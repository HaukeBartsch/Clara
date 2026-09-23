<?php
 #
 # Access Control Code using access control lists defined in passwords.json
 #
 # ToDo: implement digest based authentication
 #

 $config = json_decode(file_get_contents('/data/config/config.json'), TRUE);
 if (isset($config['LOCALTIMEZONE'])) {
   date_default_timezone_set($config['LOCALTIMEZONE']);
 }
 $fiona_version = "";
 if (isset($config['fiona_version'])) {
    $fiona_version = $config['fiona_version'];
 }

 $pw_file = "/var/www/html/php/passwords.json";
 $audit_file = "/var/www/html/server/logs/audit.log";
 $audit_on = 1;

  function audit( $what, $message, $type="INFO" ) {
    global $audit_file, $audit_on;

    if (!$audit_on)
       return;

    if (!is_writable( $audit_file )) {
       //syslog(LOG_EMERG, 'ERROR: could not write audit log file in '.$audit_file);
       return;
    }
    $e = new Exception;
    $callers = $e->getTraceAsString();
    $callers = explode("\n", $callers);
    if (strpos( $callers[1], "/var/www/html/php/AC.php" ) !== FALSE)
       return; // called by myself, don't add
    $user_context = check_logged_only();
    if ($user_context === FALSE) {
       $user_context = "unknown";
    }
    $processUser = posix_getpwuid(posix_geteuid());

    // use a JSON log
    $ar = array("timestamp" => date('Y-m-d H:i:s'), "context" => $what, "type" => $type, "caller" => $callers[1], "message" => $message, "user" => $user_context, "process_user" => $processUser['name']);
    //$str = "[".date(DATE_RFC2822)."] [".$callers[1]."] ".$what.": ".trim(preg_replace('/\s+/', ' ', $message))."\n";
    file_put_contents( $audit_file, json_encode($ar)."\n", FILE_APPEND );
  }

  function logString($what, $message, $type="INFO" ) {
    $e = new Exception;
    $callers = $e->getTraceAsString();
    $callers = explode("\n", $callers);
    $user_context = check_logged_only();
    if ($user_context === FALSE) {
       $user_context = "unknown";
    }
    $processUser = posix_getpwuid(posix_geteuid());

    $txt = $type." ".$what.": ".$message;
    $txt = str_replace("\"", "", $txt);

    $type_num = 200;
    if ($type == "INFO" || $type == "SUCCESS") {
      $type_num = 200;
    } else if ($type == "ERROR") {
      $type_num = 400;
    } else if ($type == "WARNING") {
      $type_num = 500; // does not make sense
    }

    // return a string in Common Log format
    $ar = "127.0.0.1"." "."-".$processUser['name']." "."[".date('d/m/Y:H:i:s O')."] \"".$txt."\" ".$type_num." 0";
    // array("timestamp" => date('%d/%b/%Y:%H:%M:%S %z'), "context" => $what, "type" => $type, "caller" => $callers[1], "message" => $message, "user" => $user_context, "process_user" => $processUser['name']);
    return $ar;
  }

  function loadDB() {
     global $pw_file;

     // parse permissions
     if (!file_exists($pw_file)) {
        echo ('error: permission file does not exist');
        return;
     }
     if (!is_readable($pw_file)) {
        echo ('error: cannot read file...');
        return;
     }
     $d = json_decode(file_get_contents($pw_file), true);
     if ($d == NULL) {
        echo('error: could not parse the password file');
     }

     return $d;
  }

  function saveDB( $d ) {
     global $pw_file;

     // parse permissions
     if (!file_exists($pw_file)) {
        echo ('error: permission file does not exist');
        return;
     }
     if (!is_writable($pw_file)) {
        echo ('Error: cannot write permissions file ('.$pw_file.')');
        return;
     }
     // be more careful here, we need to write first to a new file, make sure that this
     // works and copy the result over to the pw_file
     $testfn = $pw_file . '_test';
//     if (!is_writable($testfn)) {
//        syslog(LOG_EMERG, "Error: not writable ".$testfn. " for current user: ".get_current_user()." ".json_encode(debug_backtrace()));
//     }

     file_put_contents($testfn, json_encode($d));
     if (file_exists($testfn) && filesize($testfn) > 0) {
        // seems to have worked, now rename this file to pw_file
	rename($testfn, $pw_file);
     } else {
        // turn off spam to console
        //syslog(LOG_EMERG, 'ERROR: could not write file into '.$testfn.'. File size after writing is 0. Current user is: '.get_current_user());
     }
  }

  // returns a positive id of the new role if everything worked
  function addRole( $name ) {
    $d = loadDB();
    
    $found = false;
    $highestID = 0;
    foreach ($d['roles'] as $role) {
       if ($name == $role['name']) {
          $found = true;
       }
       if ($role['id'] > $highestID)
           $highestID = $role['id'];
    }
    $highestID++;
    if (!$found) {
      array_push( $d['roles'], array( "name" => $name, "id" => $highestID, "permissions" => array() ) );
      saveDB( $d );
    } else {
      $highestID = -1; // indicate error
    }
    audit( "addRole", $name, "INFO" );
    return $highestID;
  }

  // returns a positive id of the new role if everything worked
  function removeRole( $name ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['roles'] as $key => $role) {
       if ($name == $role['name']) {
          unset($d['roles'][$key]);
          $found = true;
       }
    }
    if ($found) {
      audit( "removeRole", $name, "INFO" );
      saveDB( $d );
    }
  }

  // returns a positive id of the new role if everything worked
  function addUser( $name, $email, $fullname, $organization ) {
    $d = loadDB();
    
    $found = false;
    $highestID = 0;
    foreach ($d['users'] as $user) {
       if ($name == $user['name']) {
          $found = true;
       }
       if ($email == $user['email']) {
          $found = true;
       }
       if ($user['id'] > $highestID)
           $highestID = $user['id'];
    }
    $highestID++;
    if (!$found) {
      $uuid = bin2hex(random_bytes(20));
      // TODO: pw is not defined here
      array_push( $d['users'], array( "name" => $name, "id" => $highestID, "password" => $pw, "email" => $email, "fullname" => $fullname, "organization" => $organization, "roles" => array(), "uuid" => $uuid ) );
      saveDB( $d );
      sendEmail('addUser', $email, ['uuid' => $uuid]);
    } else {
      $highestID = -1; // indicate error
    }
    audit( "addUser", $name." ".$email." ".$fullname." ".$organization, "INFO" );
    return $highestID;
  }
  
  // returns a positive id of the new role if everything worked
  // TODO: Switch to a new password storage option now (password_hash).
  function changePassword( $name, $pw ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['users'] as &$user) {
       if ($name == $user['name'] || $name == $user['email']) {
          //$user["password"] = $pw; // new style password now
          $user["password"] = password_hash($pw,PASSWORD_DEFAULT); // new style password now
          $user["lastTimePasswordChanged"] = date(DATE_RFC2822);
          // setUserVariable( $_SESSION["logged"], "lastTimeLoggedIn", date(DATE_RFC2822) );

          saveDB( $d );
          $found = true;
          break;
       }
    }
    if (!$found)
       return FALSE;
    audit( "changePassword done", $name );
    return TRUE;
  }

  // returns a positive id of the new role if everything worked
  function removeUser( $name ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['users'] as $key => $user) {
       if ($name == $user['name'] || $name == $user['email']) {
          unset($d['users'][$key]);
          $found = true;
       }
    }
    if ($found) {
      audit( "removeUser done", $name );
      saveDB( $d );
    }
  }

  // returns success
  function setUserVariable( $name, $valuekey, $valuevalue ) {

    //file_put_contents("/tmp/bla", json_encode( array( "user" => $name ) ) );
    $d = loadDB();
    
    if (is_null($d)) {
       return false; // loading database failed
    }

    $found = false;
    foreach ($d['users'] as $key => $user) {
       if ( strcmp($name, $user['name']) == 0 || 
            (isset($user['email']) && strcmp($name, $user['email']) == 0) ) {
          if (strcmp($valuevalue, "rm") == 0) {
             unset($d['users'][$key][$valuekey]);
          } else {
   	     $d['users'][$key][$valuekey] = $valuevalue;
          }
          $found = true;
       }
    }
    if ($found) {
      saveDB( $d );
      return true;
    }
    return false;
  }

  // returns a positive id of the new role if everything worked
  function getUserVariable( $name, $valuekey ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['users'] as $key => $user) {
       if (strcmp($name, $user['name']) == 0 || 
           strcmp($name, $user['email']) == 0 ) {
          //unset($d['users'][$key]);
          if (array_key_exists($valuekey, $d['users'][$key])) {
  	    return $d['users'][$key][$valuekey];
          } else
	    return FALSE;
       }
    }
    return FALSE;
  }
  
  function addPermissionToRole( $permission, $role ) {
    $d = loadDB();
    
    $permission_id = -1;
    foreach ($d['permissions'] as $key => $value) {
       if (strcmp($value['name'], $permission) == 0) {
          $permission_id = $value['id'];
       }
    }
    if ($permission_id == -1) {
       return false; // unknown role  
    }  
    
    foreach ($d['roles'] as $key => $u) {
       if ($role == $u['name']) {
          $found = false;
          foreach ($u['permissions'] as $p) {
             if ($permission_id == $p) {
                $found = true;
             }
          }
          if (!$found) {
             $d['roles'][$key]['permissions'][] = $permission_id;
             saveDB( $d );
             return true;
          }
       }
    }

    return false;  

  }
 
function getUsersByRole( $role_in ) {
    if (session_status() == PHP_SESSION_NONE) {
        session_start(); /// initialize session
    }
    $user_name = check_logged(); /// function checks if visitor is logged in.
    if (!$user_name) {
        return;
    }
    
    $d = loadDB();
    $role_id = false;
    foreach ($d['roles'] as $key => $u) {
        if ($role_in == $u["name"]) {
            $role_id = $u["id"];
            break;
        }
    }
    $users = [];
    if ($role_id == false) {
        // project could not be found
        return json_encode($users);
    }
    foreach ($d['users'] as $key => $u) {
        if (in_array($role_id, $u['roles'])) {
            $users[] = $u["name"];
        }
    }
    if (!in_array($user_name, $users)) {
       echo('Error: the current user is not a member of the project, no data will be returned.');
       return array();
    }
    return $users;
}

  function addRoleToUser( $role, $user ) {
    $d = loadDB();
    
    $role_id = -1;
    foreach ($d['roles'] as $key => $value) {
       if (strcmp($value['name'], $role) == 0) {
          $role_id = $value['id'];
       }
    }
    if ($role_id == -1) {
       return false; // unknown role  
    }
    
    foreach ($d['users'] as $key => $u) {
       if ($user == $u['name']) {
          $found = false;
          foreach ($u['roles'] as $r) {
             if ($role_id == $r) {
                $found = true;
             }
          }
          if (!$found) {
             $d['users'][$key]['roles'][] = $role_id;
             saveDB( $d );
             return true;
          }
       }
    }

    return false;  
  }
  
  function removeRoleFromUser( $role, $user ) {
    $d = loadDB();

    $role_id = -1;
    foreach ($d['roles'] as $key => $value) {
       if (strcmp($value['name'], $role) == 0) {
          $role_id = $value['id'];
       }
    }
    if ($role_id == -1) {
       return false; // unknown role                                                                                                                                                                
    }

    foreach ($d['users'] as $key => $u) {
       if ($user == $u['name']) {
         if (in_array($role_id, $u['roles'])) {
             $d['users'][$key]['roles'] = array_diff($d['users'][$key]['roles'], array( $role_id ));
             saveDB( $d );
             return true;
         }
       }
    }

    return false;
  }
  
  // returns a positive id of the new role if everything worked
  function addPermission( $name ) {
    $d = loadDB();
    
    $found = false;
    $highestID = 0;
    foreach ($d['permissions'] as $perm) {
       if ($name == $perm['name']) {
          $found = true;
       }
       if ($perm['id'] > $highestID)
           $highestID = $perm['id'];
    }
    $highestID++;
    if (!$found) {
      array_push( $d['permissions'], array( "name" => $name, "id" => $highestID ) );
      saveDB( $d );
      audit( "addPermission", $name, "SUCCESS" );
    } else {
      $highestID = -1; // indicate error
    }
    return $highestID;
  }

  // remove permission
  function removePermission( $name ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['permissions'] as $key => $perm) {
       if ($name == $perm['name']) {
          unset($d['permissions'][$key]);
          $found = true;
       }
    }
    if ($found) {
      saveDB( $d );
      audit( "removePermission", $name, "SUCCESS" );
    }
  }
  
  // remove permission
  function removePermissionFromRole( $roleName, $name ) {
    $d = loadDB();
    $id = -1;
    // what is the number of the permission?
    foreach ($d['permissions'] as $key => $perm) {
       if ( $perm['name'] == $name ) {
          $id = $perm['id'];
          break;
       }
    }
    if ($id == -1) {
       return;
    }
    
    $found = false;
    foreach ($d['roles'] as $key => $role) {
       if ($role['name'] != $roleName)
         continue;
       //syslog(LOG_EMERG, 'try to remove permission '.$name.' ('.$id.') from '.$role['name']);
       if (in_array($id, $role['permissions'])) {
            $d['roles'][$key]['permissions'] = array_diff($d['roles'][$key]['permissions'], array( $id ));
            //unset($d['roles'][$name]);
            $found = true;
       }
    }
    if ($found) {
      saveDB( $d );
      audit( "removePermissionFromRole", $roleName." ".$name, "SUCCESS" );
    }
  }
  // set the global USER array with user and passwords
  global $USERS;
  $d = loadDB();

  $USERS = array();
  foreach ( $d["users"] as $value ) {
    $USERS[ $value["name"] ] = $value["password"];
    if ( array_key_exists("email", $value) ) {
       $USERS[ $value["email"] ] = $value["password"];
    }
  }

  // does the user has a specific role?
  function check_role( $role_in ) {
    global $_SESSION;

    // read the permissions database
    $d = loadDB();
    if (is_null($d)) {
       return false; // loading database failed
    }

    // get numeric value for role_in
    $role_in_id = "";
    foreach ($d["roles"] as $key => $value) {
       if ($value["name"] == $role_in) { // check for name of role
          $role_in_id = $value["id"];
       }
    }
    if ($role_in_id == "") {
       //syslog(LOG_EMERG, "error in check_role, this role \"".$role_in."\" is not unknown (name).");
       audit( "check_role", $role_in." as ".$user_name, "FAILED");
       return false;
    }

    // check if the current user
    $user_name = $_SESSION["logged"];
    // has a specified role
    $roles = array(); // collect all roles for this user
    foreach ( $d["users"] as $key => $value ) {
      if ($value["name"] == $user_name) {
         $roles = array_merge($roles, $value["roles"]);
      }
    }
    // syslog(LOG_EMERG, "roles: ".$role_in_id." ".json_encode($roles));
    foreach ( $roles as $role ) {
       if ($role === $role_in_id) {
           // ignore if this worked
           // audit( "check_role", $role_in." as ".$user_name, "SUCCESS");
           return true;
       }
    }
    audit( "check_role", $role_in." as ".$user_name, "FAILED");
    return false;
  }

  function getUserInfo( $user_name ) {
    $user_logged_in = check_logged();
    if ($user_name != $user_logged_in && !check_role( "admin" )) {
       // do not provide any information
       return;
    }

    // read the permissions database
    $d = loadDB();
    $data = array();
    foreach ( $d["users"] as $key => $value ) {
      if ($value["name"] != $user_name) {
        continue;
      }
      if (!isset($value["organization"])) {
        $value["organization"] = "";
      }
      if (!isset($value["fullname"])) {
        $value["fullname"] = "";
      }
      $entry = array(
       	     "email" => $value["email"],
      	     "name" => $value["name"],
	     "organization" => $value["organization"],
	     "fullname" => $value["fullname"]);
      $data[] = $entry;
    }
    return $data;
  }

  function getUserNameFromEmail( $email ) {
    // read the permissions database
    $d = loadDB();

    foreach ( $d["users"] as $key => $value ) {
      if ($value["email"] == $email) {
         return $value["name"];
      }
    }
    return "unknown";
  }

  function getEmailFromUserName( $user_name ) {
    // read the permissions database
    $d = loadDB();

    foreach ( $d["users"] as $key => $value ) {
      if ($value["name"] != $user_name) {
         continue;
      }
      if (isset($value["email"])) {
         return $value["email"];
      }
    }
    return "unknown";
  }

  function getUserInfoFromUUID( $uuid ) {
    // read the permissions database
    $d = loadDB();

    foreach ( $d["users"] as $key => $value ) {
      
      if (array_key_exists('uuid', $value) && $value['uuid'] === $uuid) {
         return [
	   'name' => $value['name'],
	   'email' => $value['email']
	 ];
      }
    }

    return "unknown";
  }


  // does the user have this permissions?
  function check_permission( $permission ) {
    global $_SESSION;

    // read the permissions database
    $d = loadDB();

    // check if the current user
    $user_name = $_SESSION["logged"];
    // is allowed to use permission
    $roles = array();
    foreach ( $d["users"] as $key => $value ) {
      if ($value["name"] == $user_name) {
         $roles = array_merge($roles, $value["roles"]);
      }
    }

    // for each role find the list of permissions
    $userpermissions = array();
    foreach ($d["roles"] as $key => $value) { // all known roles      
        foreach ($roles as $role) { // roles of the current user
           if ($value["id"] == $role) {
             $userpermissions = array_merge($userpermissions, $value["permissions"]);
           }
        }
    }
    //print_r($userpermissions);
    //syslog(LOG_EMERG, "got permissions: ".json_encode($userpermissions). "      ".json_encode($d));


    // for each found permission find the name and compare to requested permission
    foreach ($userpermissions as $perm) {
       foreach ($d["permissions"] as $key => $value) {
           //syslog(LOG_EMERG, "check userpermission: ".$perm." should equal: ".$value["id"]." and requested permission: ".$permission." with name of this permission: ".$value["name"]);
           if ($perm == $value["id"] && 
               $value["name"] == $permission) {
	       // ignore if it worked, only add log entry if it failed
               // audit( "check_permission", $permission." as ".$user_name, "SUCCESS");
              return true;
           }
       }
    }

    audit( "check_permission", $permission." as ".$user_name, "FAILED");
    return false;
  }
  // has to be logged in, forwards to login page if not
  function check_logged() {
      global $_SESSION, $USERS, $_SERVER, $fiona_version;

     // What if $_SESSION["logged"] does not exist? We wil get some warnings here.

     if (!isset($_SESSION["logged"]) || !array_key_exists($_SESSION["logged"],$USERS)) {
      	$qs = $_SERVER['QUERY_STRING'];
        audit( "check_logged failed", "" );
        if ($qs != "")
           header("Location: /fiona_v".$fiona_version."/applications/User/login.php?".$_SERVER['QUERY_STRING']."&url=".$_SERVER['PHP_SELF']);
        else
           header("Location: /fiona_v".$fiona_version."/applications/User/login.php"."?url=".$_SERVER['PHP_SELF']);
	exit(); // we should never leave this loop	   
     };
     // store that this user has logged in now
     setUserVariable( $_SESSION["logged"], "lastTimeLoggedIn", date(DATE_RFC2822) );

     // do not safe a log message if we have a success
     // audit( "check_logged", "user ".$_SESSION["logged"], "SUCCESS" );
     return $_SESSION["logged"];
  };

  // Check if a user is logged in and return its name or FALSE of no user is logged in
  function check_logged_only() {
     global $_SESSION, $USERS, $_SERVER;

     if (!isset($_SESSION["logged"]) || !array_key_exists($_SESSION["logged"],$USERS)) {
        return FALSE;
     };
     return $_SESSION["logged"];
  };

  // list all users, secure function, returns nothing if user is not logged in or
  // role is not admin
  function list_users() {
    if (session_status() == PHP_SESSION_NONE) {
      session_start(); /// initialize session
    }
    $user_name = check_logged(); /// function checks if visitor is logged in.
    if (!$user_name)
       return;

    $allowed = false;
    if (!check_role( "admin" )) {
        return false;
    }

    // read the permissions database
    $d = loadDB();
    return $d["users"];
  }

  // list all users, secure function, returns nothing if user is not logged in or
  // role is not admin or if the $user_in is not the current user
  function list_roles( $user_in ) {
    if (session_status() == PHP_SESSION_NONE) {
       session_start(); /// initialize session
    }
    $user_name = check_logged(); /// function checks if visitor is logged in.
    if (!$user_name) {
       return;
    }

    $allowed = false;
    if (!check_role( "admin" ) && $user_in != $user_name) {
        return false;
    }

    // read the permissions database
    $d = loadDB();
    if ($user_in !== null) { // return role names of the current user
      foreach ($d["users"] as $key => $value) {
         if ( $value["name"] == $user_in ) {
            $role_names = array();
            foreach ($value["roles"] as $role) {
               foreach ($d["roles"] as $r) {
                 if ($role == $r["id"])
                   $role_names[] = $r["name"];
               }
            }
            return $role_names;
         }
      }
    } else { // return all role names
      $role_names = array();
      foreach ($d["roles"] as $r) {
         $role_names[] = $r['name'];
      }
      return $role_names;
    }
    return;
  }

  // list all users, secure function, returns nothing if user is not logged in or
  // role is not admin or user does not have this role
  function list_permissions( $role_in ) {
    global $_SESSION;
    if (!isset($_SESSION)) {
      session_start();
    }
    $user_name = check_logged(); /// function checks if visitor is logged in.
    if (!$user_name)
       return;

    $allowed = false;
    if (!check_role( "admin" )) {
	// the current user could have the role, in that case return the permissions - otherwise only return if you have admin rights
	$rs = list_roles($user_name);
	if (!in_array($role_in, $rs))
          return false;
    }

    // read the permissions database
    $d = loadDB();
    if ($role_in !== null) { // return role names of the current user
      foreach ($d["roles"] as $key => $value) {
         if ( $value["name"] == $role_in ) {
            $permissions_names = array();
            foreach ($value["permissions"] as $perm) {
               foreach ($d["permissions"] as $r) {
                 if ($perm == $r["id"])
                   $permissions_names[] = $r["name"];
               }
            }
            return $permissions_names;
         }
      }
    } else { // return all role names
      $permissions_names = array();
      foreach ($d["permissions"] as $r) {
         $permissions_names[] = $r['name'];
      }
      return $permissions_names;
    }
    return;
  }

  // list all users, secure function, returns nothing if user is not logged in or
  // role is not admin
  function list_permissions_for_user( $user_in ) {
    global $_SESSION;

    // read the permissions database
    $d = loadDB();

    // check if the current user
    $user_name = $_SESSION["logged"];
    // is allowed to use permission
    $roles = array();
    foreach ( $d["users"] as $key => $value ) {
      if ($value["name"] == $user_name) {
         $roles = array_merge($roles, $value["roles"]);
      }
    }

    // for each role find the list of permissions
    $userpermissions = array();
    foreach ($d["roles"] as $key => $value) { // all known roles      
        foreach ($roles as $role) { // roles of the current user
           if ($value["id"] == $role) {
             $userpermissions = array_merge($userpermissions, $value["permissions"]);
           }
        }
    }
    
    // for each found permission find the name
    $userpermissions_str = array(); // array name instead of id
    foreach ($userpermissions as $perm) {
       foreach ($d["permissions"] as $key => $value) {
           if ($perm == $value["id"]) {
              $userpermissions_str[] = $value["name"];
           }
       }
    }
        
    return $userpermissions_str;
  }

  function changeEmail( $name, $email ) {
    $d = loadDB();
    
    $found = false;
    foreach ($d['users'] as &$user) {
       if ($name == $user['name']) {
          $user["email"] = $email;
          saveDB( $d );
          $found = true;
          break;
       }
    }
    if (!$found)
       return FALSE;
    audit( "changeEmail", $name, "SUCCESS" );
    return TRUE;
  }

  function removeUUID( $name ) {
    $d = loadDB();
    
    $found = false;

    foreach ($d['users'] as &$user) {
       if ($name == $user['name']) {
         if (!array_key_exists('uuid', $user)) {
	   return false;
	 }
	 
         unset($user["uuid"]);
         saveDB( $d );
         $found = true;
         break;
       }
    }
    
    if (!$found) {
      return false;
    }

    audit( "removeUUID", $name, "SUCCESS" );

    return true;
  }

  function sendEmail($action, $email, $params = []) {
    $from = 'no-reply@helse-bergen.no';
      
    // Create email headers
    $headers  = 'MIME-Version: 1.0' . "\r\n";
    $headers .= 'Content-type: text/html; charset=iso-8859-1' . "\r\n";
    $headers .= 'From: '.$from."\r\n".
    	  'Reply-To: '.$from."\r\n" .
    	  'X-Mailer: PHP/' . phpversion();

    if ($action === 'addUser') {
      $title = 'Set new password';
      $uuid = array_key_exists('uuid', $params) ? $params['uuid'] : '';
      $link = '<a href="https://fiona.ihelse.net/applications/User/setPassword.php?uuid='.$uuid.'">Set password</a>';
      $message = "This email was generated by FIONA.ihelse.net.<br/><br/>Click on the (one-time) link below in order to set a new password.<br>$link";
    } else if ($action === 'newPassword') {
      $title = 'New password';
      $newPassword = $params['password'] ?? '';
      $message = "This email was generated by FIONA.ihelse.net.<br/><br/>A new password has been created and set for your profile. Login using this password: $newPassword";
    } else {
      audit('sendEmail', 'Unknown action.\n', "FAILED");
      return false;
    }

    if (mail($email, $title, $message, $headers)) {
      audit('sendEmail', 'Email was sent successfully', "SUCCESS");
      return true;
    } else {
      audit('sendEmail', 'Error while sending email.', "FAILURE");
      return false;
    }
  }
  
?>

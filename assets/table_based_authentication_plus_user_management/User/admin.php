<?php
  if (session_status() == PHP_SESSION_NONE) {
    session_start(); /// initialize session
  }
  include("../../php/AC.php");
  $user_name = check_logged(); /// function checks if visitor is logged.
  echo('<script type="text/javascript"> user_name = "'.$user_name.'"; </script>'."\n");

  $allowed = false;
  if (check_role( "admin" )) {
     echo('<script type="text/javascript"> role = "admin"; </script>'."\n");    
     $allowed = true;
  }

  $r = 'requests'; // collect .json files from the request directory to construct a table of current requests
  $req = array();
  if (is_dir($r) && is_readable($r)) {
    if ($handle = opendir($r)) {
      while (false !== ($entry = readdir($handle))) {
        $file_parts = pathinfo($entry);
        if ($entry != "." && $entry != ".." && $file_parts['extension'] == 'json') {
           $req[] = json_decode(file_get_contents( $r."/".$entry ), true );
        }
      }
      closedir($handle);
    }
  }

?>
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<meta name="description" content="Upload data portal.">
	<title>FIONA Admin Screen</title>

	<!--[if lt IE 9]>
		<script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
	<![endif]-->


        <link href="css/select2.min.css" rel="stylesheet">
        <link href="css/bootstrap.min.css" rel="stylesheet">
<!--	<link href="css/bootstrap-responsive.min.css" rel="stylesheet"> -->
	<link href="css/font-awesome.min.css" rel="stylesheet">
<!-- 	<link href="css/bootswatch.css" rel="stylesheet"> -->
       <!-- <link href="css/jquery-ui.css" rel="stylesheet" type="text/css"/> -->
        <!-- HTML5 shim, for IE6-8 support of HTML5 elements -->
        <!--[if lt IE 9]>
          <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
        <![endif]-->

        <style type="text/css">
           .index h3 {
              text-align: left;
           }
           .footer {
              height: 2em;
           }
           #user-role-processing span {
               width: 200px;
           }
           #roles-section-ui h3 {
              width: 100%;
           }
	   
	   .user-role-add {
	     border: 1px solid gray;
	     border-radius: 2px;
	   }
           #user-section-ui h3 {
              width: 100%;
           }
           .close {
              float: none;
	      font-size: 1.4em;
           }
           .modal-header .close {
              float: right;
           }
	   .btn-info {
	      width: 100%;
	   }
           li h3 .close {
              float: right;
           }
//	   li h3.btn {
//	      vertical-align: top;
//	   }	   
	   .user-row {
	     margin-bottom: 5px;
	     padding: 2px;
	   }
	   .permissions-row {
	      margin-bottom: 2px;
	   }
	   .permissions-row.btn-info {
	      width: 33%;
	   }
	   .permissions-row.btn-warning {
	      width: 33%;
	   }
	   ul {
	     padding: 0px;
	   }
           .permissions-row .close {
              float: right;
           }
	   .roles-row {
	      margin-bottom: 5px;
	      padding: 2px;
	   }
           input[type="text"] {
              height: 30px;
           }
           input[type="email"] {
              height: 30px;
           }
           input[type="password"] {
              height: 30px;
           }
           .nav li {
              background-color: black;
           }
           li {
              background-color: #EEEEEE;
              border-radius: 1px 1px 1px 1px;
              padding-left: 5px;
              width: 100%;
              min-width: 100px;
	      list-style-type: none;
           }
	   li div {
	     font-size: .8em;
	     line-height: 1em;
	     margin-bottom: 5px;
	     position: relative;
	   }
	   li div span {
	     border-radius: 3px;
	     border: 1px solid grey;
	     padding-left:3px;
	     margin-bottom: 2px;
	     line-height: 1.7em;
	   }
           td {
	      vertical-align: top;
           }
           i {
              padding-right: 5px;
           }
        </style>
</head>
<body class="index" id="top">

<header>
  <div class="collapse bg-dark" id="navbarHeader">
    <div class="container-fluid">
      <div class="row">
        <div class="col-sm-8 col-md-7 py-4">
          <h4 class="text-white">About</h4>
          <p class="text-muted">This application allows the admin to set permissions by roles.</p>
        </div>
        <div class="col-sm-4 offset-md-1 py-4">
          <h4 class="text-white">Contact</h4>
          <ul>
            <li style="background-color: transparent;" class="text-muted">Email <a href="mailto:Hauke.Bartsch@helse-bergen.no" class="text-white">Hauke Bartsch</a></li>
          </ul>
        </div>
        <div class="col-sm-8 col-md-7 py-4" style="padding-top: 0px !important;">
          <h4 class="text-white">Admin interface</h4>
	  <p class="text-muted">Assign and create permissions, assign them to roles and the roles to users. Create new user accounts (sends out an email to create a password).</p>
	</div>
      </div>
    </div>
  </div>
  <div class="navbar navbar-dark bg-dark shadow-sm">
    <div class="container-fluid d-flex justify-content-between">
      <a href="#" class="navbar-brand d-flex align-items-center">
        FIONA Admin User Permissions
      </a>
      <a href="/index.php" class="d-flex align-items-center nav-link navbar-text" title="Back to Fiona">
        Fiona
      </a>
      <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarHeader" aria-controls="navbarHeader" aria-expanded="false" aria-label="Toggle navigation">
        <span class="navbar-toggler-icon"></span>
      </button>
    </div>
  </div>
</header>


  <div class="container-fluid">
    
    <?php if ( !$allowed ) { ?>
    <div class="row">
        <p>The current user has no permissions to administer this portal.</p>
    </div>
    <?php } else { ?>
        <p>This page requires administrator privileges.
        The current user has permissions to administer this portal.</p>
<div class="row">
<div class="col">

        <h3>Users</h3>
        <div style="position: relative;">
          <button class="btn btn-primary" style="margin-bottom: 5px;" type="button" data-toggle="modal" data-target="#add-new-user-dialog"><i class="icon-plus-sign"></i>Create new user</button>
	  <div class="form" style="position: absolute; right: 0px; top: 0px;">
                <div class="input-group">
                  <input type="text" class="form-control" placeholder="Search user" id="searchUsers">
 		  <span class="input-group-append">
                    <button class="btn bg-transparent" type="button" style="margin-left: -40px; margin-top: -13px; z-index: 100;" title="Clear search field" id="clear-searchUsers">
                        <i style="font-size: 24px;">&times;</i>
                    </button>
                  </span>
                </div>
	  </div>	  
        </div>
        <div id="user-section-ui" style="max-height: 600px; overflow-y: auto; background-color: orange;"></div>
</div>
<div class="col">
        <h3>Roles</h3>
        <div style="position: relative;">
          <button class="btn btn-primary" type="button" style="margin-bottom: 5px;" data-toggle="modal" data-target="#add-new-role-dialog"><i class="icon-plus-sign"></i>Create new role</button>
	  <div class="form" style="position: absolute; right: 0px; top: 0px;">
                <div class="input-group">
                  <input type="text" class="form-control" placeholder="Search role name" id="searchRoles">
 		  <span class="input-group-append">
                    <button class="btn bg-transparent" type="button" style="margin-left: -40px; margin-top: -13px; z-index: 100;" title="Clear search field" id="clear-searchRoles">
                        <i style="font-size: 24px;">&times;</i>
                    </button>
                  </span>
                </div>
	  </div>	  
        </div>
        <div id="roles-section-ui" style="max-height: 600px; overflow-y: auto; background-color: yellow;"></div>
</div>
</div>
<hr>
<div class="row">
  <div class="col-12">
        <h3>Permissions</h3>
        <div style="position: relative;">
          <button class="btn btn-primary"  type="button" data-toggle="modal" data-target="#add-new-permission-dialog"><i class="icon-plus-sign"></i>Create new permission</button>
	  <div class="form" style="position: absolute; right: 0px; top: 0px;">
                <div class="input-group">
                  <input type="text" class="form-control" placeholder="Search permission name" id="searchPermissions">
 		  <span class="input-group-append">
                    <button class="btn bg-transparent" type="button" style="margin-left: -40px; margin-top: -13px; z-index: 100;" title="Clear search field" id="clear-searchPermissions">
                        <i style="font-size: 24px;">&times;</i>
                    </button>
                  </span>
                </div>
	  </div>

        </div><br/>
        <div id="permissions-section-ui"></div>
  </div>
</div>
        <hr>
    <div class="footer"></div>    
    <?php } ?>
    <script type="text/x-tmpl" id="user-section">
      <ul>
      {% for (var i = 0; i < o.length; i++) { %}
        <li class="user-row">
          <h3 class="btn btn-info" title="email: {%=o[i].email%}, lastKnownLogin: {%=o[i].lastTimeLoggedIn%}">{%=o[i].name%} <button class="close" onclick="removeUser('{%=o[i].name%}');">&times;</button><br/><small style="color: white;">{%=o[i].fullname%}, {%=o[i].organization%}, {%=o[i].email%}</small></h3>
          <div id="user-role-{%=o[i].name%}"></div>
          <button class="btn btn-primary btn-sm user-role-add" user="{%=o[i].name%}" data-toggle="modal" data-target="#add-role-to-user-dialog" onclick="jQuery('#add-role-to-user-user-name').attr('user', '{%=o[i].name%}');"><i class="icon-plus-sign"></i>Add Role</div>
        </li>
      {% } %}
      </ul>
    </script>
    <script type="text/x-tmpl" id="roles-section">
      <ul>
      {% for (var i = 0; i < o.length; i++) { %}
        <li class="roles-row">
          <h3 class="btn btn-info">{%=o[i]%}<button class="close" onclick="removeRole('{%=o[i]%}');">&times;</button></h3>
          <div id="permission-role-{%=o[i]%}"></div>  
          <button class="btn btn-primary btn-sm role-permission-add" user="{%=o[i]%}" data-toggle="modal" data-target="#add-permission-to-role-dialog" onclick="jQuery('#add-permission-to-role-role-name').attr('role', '{%=o[i]%}');"><i class="icon-plus-sign"></i>Add Permission</div>
        </li>
      {% } %}
      </ul>
    </script>    
    <script type="text/x-tmpl" id="permissions-section">
      <ul>
      {% for (var i = 0; i < o.length; i++) { %}
      {% var c = "btn-info"; if (o[i].startsWith('Project')) { c = "btn-warning"; } %}
        <li class="permissions-row btn {%=c%} btn-sm">{%=o[i]%}<button class="close" onclick="removePermission('{%=o[i]%}');">&times;</button></li>
      {% } %}
      </ul>
    </script>    
    <script type="text/x-tmpl" id="role-selection">
      <div id="role-selection-radio" class="btn-group" data-toggle="buttons-radio" style="display: none;">
      {% for (var i = 0; i < o.length; i++) { %}
        <button type="button" class="btn btn-primary btn-sm role-selection-radio-entry" value="{%=o[i]%}">{%=o[i]%}</button>
      {% } %}
      </div>
      <div class="mb-12">
      <label for="role-selection-select2" class="form-label">Select an existing role</label>      
      <select id="role-selection-select2" name="roles" style="width: 100%;">
         <option value="">&nbsp;</option>
      {% for (var i = 0; i < o.length; i++) { %}
        <option value="{%=o[i]%}">{%=o[i]%}</option>
      {% } %}
      </select>
    </script>    
    <script type="text/x-tmpl" id="permissions-selection">
      <div id="permission-selection-radio" class="btn-group" data-toggle="buttons-radio" style="display: none;">
      {% for (var i = 0; i < o.length; i++) { %}
        <button type="button" class="btn btn-primary btn-sm permission-selection-radio-entry" value="{%=o[i]%}">{%=o[i]%}</button>
      {% } %}
      </div>

        <div class="mb-12">
          <label for="permission-selection-select2" class="form-label">Select an existing permission</label>
          <select id="permission-selection-select2" name="permissions" style="width: 100%;">
           <option value="">&nbsp;</option>
          {% for (var i = 0; i < o.length; i++) { %}
            <option value="{%=o[i]%}">{%=o[i]%}</option>
          {% } %}
          </select>
	</div>

    </script>    
  </div>

  <div id="add-permission-to-role-dialog" class="modal hide fade" role="dialog" aria-hidden="true">
    <div class="modal-dialog" role="document">
     <div class="modal-content">
       <div class="modal-header">
           <h5 class="modal-title">Add a permission to role</h5>
           <button type="button" class="close" data-dismiss="modal" aria-hidden="true">&times;</button>
       </div>
       <div class="modal-body">
         <div id="permissions-selection-ui"></div>
         <input type="hidden" id="add-permission-to-role-role-name">
       </div>
       <div class="modal-footer">
         <a href="#" class="btn" data-dismiss="modal">Close</a>
         <a href="#" class="btn btn-primary" onclick="addPermissionToRole();" data-dismiss="modal">Save</a>
       </div>
     </div>
   </div>
  </div>
  <div id="add-role-to-user-dialog" class="modal hide fade" role="dialog" aria-hidden="true">
    <div class="modal-dialog" role="document">
     <div class="modal-content">
       <div class="modal-header">
           <h5 class="modal-title">Add a role to user</h5>
           <button type="button" class="close" data-dismiss="modal" aria-hidden="true">&times;</button>
       </div>
       <div class="modal-body">
         <div id="role-selection-ui"></div>
         <input type="hidden" id="add-role-to-user-user-name">
         <!-- <input id="add-role-name" type="text" placeholder="role name"> -->
       </div>
       <div class="modal-footer">
         <a href="#" class="btn" data-dismiss="modal">Close</a>
         <a href="#" class="btn btn-primary" onclick="addRoleToUser();" data-dismiss="modal">Save</a>
       </div>
     </div>
   </div>
  </div>
  <div id="add-new-user-dialog" class="modal" role="dialog" aria-hidden="true">
    <div class="modal-dialog" role="document">
     <div class="modal-content">
       <div class="modal-header">
           <h5 class="modal-title">Add a new user</h5>
           <button type="button" class="close" data-dismiss="modal" aria-hidden="true">&times;</button>
       </div>
       <div class="modal-body">
         <div class="form-group">
	   <label for="add-user-name">User name</label>
	   <input class="form-control" id="add-user-name"     type="text"     placeholder="user name" autofocus><br/>
	 </div>
         <div class="form-group">
	   <label for="add-user-full-name">Full name</label>
           <input class="form-control" id="add-user-full-name" type="text"    placeholder="full name"><br/>
	 </div>
         <div class="form-group">
	   <label for="add-user-organization">Organization</label>
           <input class="form-control" id="add-user-organisation" type="text" placeholder="organization"><br/>
	 </div>
         <div class="form-group">
 	   <label for="add-user-email">User email</label>
           <input class="form-control" id="add-user-email"    type="email"    placeholder="email"><br/>
	 </div>
       </div>
       <div class="modal-footer">
         <a href="#" class="btn" data-dismiss="modal">Close</a>
         <a href="#" class="btn btn-primary" onclick="addUser();" data-dismiss="modal">Create</a>
       </div>
     </div>
   </div>
  </div>
  <div id="add-new-role-dialog" class="modal" role="dialog" aria-hidden="true">
    <div class="modal-dialog" role="document">
     <div class="modal-content">
       <div class="modal-header">
           <h5 class="modal-title">Add a new Role</h5>
           <button type="button" class="close" data-dismiss="modal" aria-hidden="true">&times;</button>
       </div>
       <div class="modal-body">
         <div class="form-group">
	   <label for="add-role-name">Role name</label>
	   <input class="form-control" id="add-role-name" type="text" placeholder="role name" autofocus>
	 </div>
       </div>
       <div class="modal-footer">
         <a href="#" class="btn" data-dismiss="modal">Close</a>
         <a href="#" class="btn btn-primary" onclick="addRole();" data-dismiss="modal">Save</a>
       </div>
     </div>
   </div>
  </div>
  <div id="add-new-permission-dialog" class="modal" role="dialog" aria-hidden="true" tabindex="-1">
    <div class="modal-dialog" role="document">
     <div class="modal-content">
       <div class="modal-header">
           <h5 class="modal-title">Add a new permission</h5>
           <button type="button" class="close" data-dismiss="modal" aria-label="Close"><span aria-hidden="true">&times;</span></button>
       </div>
       <div class="modal-body">
         <div class="form-group">
	   <label for="add-permission-name">Name of the new permission:</label>
           <input id="add-permission-name" class="form-control" type="text" placeholder="permission name" autofocus>
	 </div>
       </div>
       <div class="modal-footer">
         <a href="#" class="btn" data-dismiss="modal">Close</a>
         <a href="#" class="btn btn-primary" onclick="addPermission();" data-dismiss="modal">Save</a>
       </div>
     </div>
   </div>
  </div>

  <script src="js/jquery-3.7.1.min.js"></script>
  <script src="js/select2.min.js"></script>
 <!-- <script src="js/jquery-ui.min.js"></script> -->
  <script type="text/javascript" src="js/tmpl.min.js"></script>
  <script type="text/javascript" src="/js/md5-min.js"></script>


  <script type="text/javascript">

    function addPermissionToRole() {
       var user = jQuery('#add-permission-to-role-role-name').attr('role');
       var name = jQuery(".permission-selection-radio-entry[class*='active']").val();
       if (typeof name == "undefined") {
           name = jQuery('#permission-selection-select2').val();
	   if (typeof name == "undefined" || name == "") {
	      return;
	   }
       }

       // remove all white spaces for now
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getRoles.php?action=addPermission&value='+name+'&value2='+user, function(data) {
          console.log('worked or not' + data);
          update();
          //jQuery('#add-permission-to-role-dialog').dialog('close');
       });
    }
    function addRoleToUser() {
       var user = jQuery('#add-role-to-user-user-name').attr('user');
       var name = jQuery(".role-selection-radio-entry[class*='active']").val();
       if (typeof name == "undefined") {
       	   name = jQuery('#role-selection-select2').val();
	   if (typeof name == "undefined" || name == "")
	      return; // no role selected
       }

       // remove all white spaces for now
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getUser.php?action=addRole&value='+name+'&value2='+user, function(data) {
          console.log('worked or not: ' + JSON.stringify(data));
          update();
          //jQuery('#add-role-to-user-dialog').dialog('close');
       });
    }
    function addRole() {
       var name = jQuery('#add-role-name').val();
       // remove all white spaces for now
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getRoles.php?action=create&value='+name, function(data) {
          console.log('worked or not' + data);
          update();
          //jQuery('#add-new-role-dialog').dialog('close');
       });
    }
    function removeRoleFromUser( name, name2 ) {
         name = name.replace(/\ /g, "");
         jQuery.getJSON('/php/getUser.php?action=removeRole&value='+name+'&value2='+name2, function(data) {
            console.log('worked or not' + data);
            update();
         });         
    }    
    function removeRole( name ) {
         name = name.replace(/\ /g, "");
         jQuery.getJSON('/php/getRoles.php?action=remove&value='+name, function(data) {
            console.log('worked or not' + data);
            update();
         });
    }
    function addUser() {
       var name = jQuery('#add-user-name').val();
       if (name == "") {
          alert("Error: user name is empty");
          return;
       }
       if (name.indexOf(".") != -1) {
	 alert("Error: user name should not contain a dot");
	 return;
       }
       if (name.indexOf(" ") != -1) {
	 alert("Error: user name should not contain a space character");
	 return;
       }
       var email = jQuery('#add-user-email').val();
       if (email == "") {
          alert("Error: email field is empty");
          return;
       }
       var fullname = jQuery('#add-user-full-name').val();
       var organization = jQuery('#add-user-organisation').val();
       
       // var password = jQuery('#add-user-password').val();
       // hash = hex_md5(password);
       // if (password == "") {
       //    alert("Error: password cannot be empty");
       //    return;
       // }
         // <div class="form-group">
	 //   <label for="add-user-password">Password</label>
         //   <input class="form-control" id="add-user-password" type="password" placeholder="password">
	 // </div>


       // remove all white spaces for now
       name  = name.replace(/\ /g, "");
       email = email.replace(/\ /g, "");
       // better use a post here to transmit data
       
       jQuery.post('/php/getUser.php?action=create&value='+name+'&value2='+email, {"organization": organization, "fullname": fullname }, 
          function(data) {
             console.log('worked or not' + data);
             update();
             // jQuery('#add-new-user-dialog').dialog('close');
          }, "json");
       jQuery('#add-user-name').val('');
       jQuery('#add-user-email').val('');
       jQuery('#add-user-password').val('');
       jQuery('#add-user-organisation').val('');
       jQuery('#add-user-full-name').val('');
    }

    function removeUser( name ) {
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getUser.php?action=remove&value='+name, function(data) {
          console.log('worked or not' + data);
          update();
       });
    }
    function addPermission() {
       var name = jQuery('#add-permission-name').val();
       // remove all white spaces for now
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getPermissions.php?action=create&value='+name, function(data) {
          console.log('worked or not' + data);
          update();
          jQuery('#add-new-permission-dialog').dialog('close');
       });
    }

    function removePermission( name ) {
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getPermissions.php?action=remove&value='+name, function(data) {
          console.log('worked or not' + data);
          update();
       });
    }
    
    function removePermissionFromRole( role, name ) {
       name = name.replace(/\ /g, "");
       jQuery.getJSON('/php/getPermissions.php?action=remove&role='+role+'&value='+name, function(data) {
          console.log('worked or not' + data);
          update();
       });
    }

    // update everything
    function update() {
       // call list of documents
       jQuery.getJSON('/php/getUser.php', function(data) {
         dataAsArray = [];
         for (key in data) {
           dataAsArray.push(data[key]);
         }

         document.getElementById("user-section-ui").innerHTML = tmpl("user-section", dataAsArray);
         var callbacks = [];
         function createCallback(data, i) {
              return function(data2) {
                   var str = "Roles:<br>";
                   for (var j = 0; j < data2.length; j++) {
                      str += "<span style='white-space: nowrap;'>" + data2[j] + "<i class='close' onclick=\"removeRoleFromUser('" + data2[j] + "', '" + data[i]['name'] + "');\">&times;</i></span>";
                      if (j < data2.length-1)
                         str += " ";
                   }
                   jQuery('#user-role-'+data[i]['name']).html(str);
              };         
         }
         
         for (var i = 0; i < dataAsArray.length; i++) {
           callbacks[i] = createCallback(dataAsArray, i);
         }
         for (var i = 0; i < dataAsArray.length; i++) {
             jQuery.getJSON('/php/getRoles.php?user_name='+dataAsArray[i]['name'], callbacks[i]);
         }
	 setTimeout(function() {
	   jQuery('#role-selection-select2').select2();
	 }, 1000);

       });
       jQuery.getJSON('/php/getRoles.php', function(data) {
         document.getElementById("roles-section-ui").innerHTML = tmpl("roles-section", data);
         document.getElementById("role-selection-ui").innerHTML = tmpl("role-selection", data);
         var callbacks2 = [];
         function createCallback2(data, i) {
              return function(data2) {
                   var str = "Permissions:<br>";
                   for (var j = 0; j < data2.length; j++) {
                      str += "<span style='white-space: nowrap;'>" + data2[j] + "<i class='close' onclick=\"removePermissionFromRole('" + data[i] + "', '" + data2[j] + "');\">&times;</i></span>";
                      if (j < data2.length-1)
                         str += ", ";
                   }
                   jQuery('#permission-role-'+data[i]).html(str);                                 
              };         
         }
         
         for (var i = 0; i < data.length; i++) {
           callbacks2[i] = createCallback2(data, i);
         }         
         for (var i = 0; i < data.length; i++) {
             jQuery.getJSON('/php/getPermissions.php?role='+data[i], callbacks2[i]);
         }
	 setTimeout(function() {
	   jQuery('#permission-selection-select2').select2();
	 }, 1000);

       });
       // add list of all permissions
       jQuery.getJSON('/php/getPermissions.php', function(data) {
         document.getElementById("permissions-section-ui").innerHTML = tmpl("permissions-section", data);
         document.getElementById("permissions-selection-ui").innerHTML = tmpl("permissions-selection", data);
       });
    }
    
    jQuery(document).ready(function() {
       update();        
       jQuery('#user-section-ui button .user-role-add').on('click', function() {
       
           // add the user name to the user-role-add-user-user-name
           var name = jQuery(this).attr('user');
           jQuery('#add-role-to-user-user-name').val(name);
       });
       jQuery('#permission-section-ui button .role-permission-add').on('click', function() {
           var name = jQuery(this).attr('role');
           jQuery('#add-permission-to-role-role-name').val(name);
       });
       //jQuery('#TABLE1').tableFilter();

       jQuery('.modal').on('shown', function() {
           jQuery(this).find("[autofocus]:first").focus();
       });

       //
       // add search functionality
       //
       jQuery('#searchUsers').on('keyup', function() {
          var searchFor = jQuery(this).val();
	  console.log("searching for " + searchFor);
	  jQuery('#user-section-ui li.user-row').each(function(idx, a) {
	     var txt = jQuery(a).find('h3').text();
	     var re = new RegExp(searchFor);
	     if (re.test(txt)) {
	        jQuery(this).show();
	     } else {
	        jQuery(this).hide();
	     }
	  });
       });
       jQuery('#clear-searchUsers').on('click', function() {
          jQuery('#searchUsers').val("");
	  jQuery('#user-section-ui li.user-row').each(function(idx, a) {
	     jQuery(this).show();
	  });
       });
       jQuery('#searchRoles').on('keyup', function() {
          var searchFor = jQuery(this).val();
	  console.log("searching for " + searchFor);
	  jQuery('#roles-section-ui li.roles-row').each(function(idx, a) {
	     var txt = jQuery(a).find('h3').text();
	     var re = new RegExp(searchFor);
	     if (re.test(txt)) {
	        jQuery(this).show();
	     } else {
	        jQuery(this).hide();
	     }
	  });
       });
       jQuery('#clear-searchRoles').on('click', function() {
          jQuery('#searchRoles').val("");
	  jQuery('#roles-section-ui li.roles-row').each(function(idx, a) {
	     jQuery(this).show();
	  });
       });
       jQuery('#searchPermissions').on('keyup', function() {
          var searchFor = jQuery(this).val();
	  console.log("searching for " + searchFor);
	  jQuery('#permissions-section-ui li.permissions-row').each(function(idx, a) {
	     var txt = jQuery(a).text();
	     var re = new RegExp(searchFor);
	     if (re.test(txt)) {
	        jQuery(this).show();
	     } else {
	        jQuery(this).hide();
	     }
	  });
       });
       jQuery('#clear-searchPermissions').on('click', function() {
          jQuery('#searchPermissions').val("");
	  jQuery('#permissions-section-ui li.permissions-row').each(function(idx, a) {
	     jQuery(this).show();
	  });
       });

    });
  </script>

  <script src="js/bootstrap.min.js"></script>
  <!-- <script src="js/bootswatch.js"></script> -->
 <!--  <script src="/js/bootstrap-modal.js"></script> -->

</body>
</html>

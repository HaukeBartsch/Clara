<?php
if (session_status() == PHP_SESSION_NONE) {
   session_start();
}
include("../../php/AC.php");

// TODO: create a two stage login to not send same hash of PW over the cable
//       1) ask for a random key from the server first (valid for the session)
//       2) use the random key and the hash of the password to compute the hash send to server
//       3) on server check key and hash if they match

// TODO: add ldap following
//       https://stackoverflow.com/questions/46858591/creating-a-php-script-that-will-check-for-username-and-password-combo-in-ldap
$username = "";
if (isset($_POST["username"])) {
   $username = $_POST["username"];
   $username = strtolower($username);
}

$pw_incorrect = false;
if (isset($_POST["ac"]) && $_POST["ac"]=="log") { /// do after login form is submitted
   // There are now two ways the password can be correct. Either its the old-style password
   // and we simply compare the hashes:
   $password_is_correct = false;
   if ($USERS[$username]===$_POST["pw"]) {
      $password_is_correct = true;
   }
   // Or we need to use password_verify to check instead.
   if (!$password_is_correct && password_verify($_POST["pw"], $USERS[$username])) {
      $password_is_correct = true;
   }
   
   //if ($USERS[$username]===$_POST["pw"]) { /// check if submitted username and password exist in $USERS array
   if ($password_is_correct === TRUE) {
       // as a features users can login using their email address
       // here we need to get the real user name and use that instead of the email address
       if (strpos($username, "@") !== false) {
           // found email as user name, what is the real name?
           $_SESSION["logged"]=getUserNameFromEmail($username);
       } else {
           $_SESSION["logged"]=$username;
       }
   } else {
       audit( "login", "incorrect password for ".$_POST["username"] );
       //echo('<script type="text/javascript">incorrect_username_or_password = true;</script>');
       $pw_incorrect = true;
       // if we are not logged in we should remove the session variables with username and password again
       if (isset($_POST['ac'])) {
          unset($_POST['ac']);
       }
       if (isset($_POST['pw'])) {
           unset($_POST['pw']);
       }
       if (isset($_POST['url']) && $_POST['url'] == "") {
           $_POST['url'] = "/index.php";
       }
   };
};
if (isset($_SESSION["logged"]) && array_key_exists($_SESSION["logged"],$USERS)) {
     //syslog(LOG_EMERG, "found url as ".$_POST['url']);
    if (isset($_POST["url"]) && $_POST["url"] != "") {
        $u = htmlentities($_POST["url"]);
    } else if (isset($_GET["url"])) {
        $u = htmlentities($_GET["url"]);        
    } else {
        $u = "/index.php";
    }
    header("Location: ".$u); // if user is logged go to front page
    exit();
}
?>

<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<meta name="description" content="Login to Steve Project Site">
	<title>Login to Steve Project Site</title>

	<!--[if lt IE 9]>
		<script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
	<![endif]-->

	<link href="css/bootstrap.css" rel="stylesheet">
	<link href="css/bootstrap-responsive.min.css" rel="stylesheet">
	<link href="css/font-awesome.min.css" rel="stylesheet">
	<link href="css/bootswatch.css" rel="stylesheet">
        <!-- <link href="css/jquery-ui.css" rel="stylesheet" type="text/css"/> -->
        <!-- HTML5 shim, for IE6-8 support of HTML5 elements -->
        <!--[if lt IE 9]>
          <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
        <![endif]-->

<?php if ($pw_incorrect): ?>
   <script type="text/javascript">incorrect_username_or_password = true;</script>
<?php endif; ?>

            
</head>
<body class="index" id="top">
  <div class="container">
   <div class="row">
     <div class="hero-unit">
       <h1>Steve Project Login</h2>
       <p class="lead">
          <center>
	     A service provided by the Mohn Medical Imaging and Visualization Centre of Bergen, Norway
	  </center>
       </p>
     </div>
   </div>
   <div class="row">
    <div class="span4"> </div>
    <div class="span3">
<!-- Try to check if a url has been provided as a get argument. Redirection should still work after failures of login. -->
<?php if (isset($_GET['url']) && explode("/", $_GET['url'])[1] == "applications"): ?>
    <form action="login.php?url=<?php echo($_GET['url']); ?>" method="post" id="login-form">
<?php else: ?>
      <form action="login.php" method="post" id="login-form">
<?php endif; ?>
         <input type="hidden" name="ac" value="log">
         <input type="hidden" name="pw" id="pw">
         <input type="hidden" name="url" id="url" value="<?php echo $_GET['url'] ?? ''; ?>">
         <input type="text" name="username" placeholder="user" class="span3" autofocus/>
      </form>
      <input id="pw-field" type="password" name="password" placeholder="********" onkeypress="handleKeyPress(event, this.form)" class="span3"><br/>
      <div align="right">
         <input type="submit" class="btn" value="Login" form="login-form" class="span3"/><br>
         <!-- <a href="/applications/User/requestLogin.php" class="small">request access</a> /<a href="/applications/User/newPassword.php" class="small">send new password</a> -->
      </div>
      <a href="https://fiona.ihelse.net/applications/User/newPassword.php" class="">Forgot password? / Glemt passord?</a>
    </div>
   </div>
  </div>
  
  <script src="/js/jquery-2.1.4.min.js"></script>
  <script src="js/jquery-ui.min.js"></script>
  <!-- create an md5sum version of the password before sending it -->
  <script src="/js/md5-min.js"></script>

  <script type="text/javascript">
    jQuery(document).ready(function() {
        
        // prevent enter in the user field to submit form
        jQuery('input').keydown(function(event) {
            if (event.keyCode == 13) {
                if (jQuery(this).attr('name') == "username") {
                    event.preventDefault();
                    jQuery('#pw-field').focus();
                    return false;
                }
            }
        });
        
        // calculate and copy hash value after entering password
        jQuery('#pw-field').blur(function() {
            rewritePW();
        });
        
        if (typeof incorrect_username_or_password != "undefined") {
            // show the message some time after the rendering of the page
            setTimeout( function() {
                alert("Incorrect username or password, please try again.");
            }, 500);
        }        
    });

function rewritePW() {
    hash = hex_md5(jQuery('#pw-field').val());
    jQuery('#pw').val(hash);     
}

function handleKeyPress(e, form) {
    var key = e.keyCode || e.which;
    if (key == 13) {
        rewritePW();
        jQuery('#login-form').submit();
    }
}

</script>
    
</body>
</html>

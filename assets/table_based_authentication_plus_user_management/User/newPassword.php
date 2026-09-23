<?php
session_start();
include("../../php/AC.php");

echo "<script>messageTxt = \"\";</script>";

if (isset($_POST["email"])) {
   // Check if this user exists
   $email = $_POST["email"];
   $name = getUserNameFromEmail($email);
   //echo "Found this name: ".$name." in our records";
   if ($name == "unknown") {
      audit( "newPassword", "account does not exist ".$_POST["email"], "FAILED" );
      echo "<script>messageTxt = \"This account does not exist!\";</script>";
   } else {
      // create a new password for this account and send to user
      audit( "newPassword", "send to ".$_POST["email"] );
      $pw = substr(uniqid(), 0, 8);
      $md5version = md5($pw);
      changePassword( $name, $md5version ); // should'nt this be a one time password?
      // email user
      sendEmail('newPassword', $email, ['password' => $pw]);
      echo "<script>messageTxt = \"After Submit an email will been sent to your account. Try to login again <a href='/applications/User/login.php'>here</a>.\";</script>. Remember after logging in to change your password.";
      header('Location: https://fiona.ihelse.net/applications/User/newPassword.php');
   }
}

?>

<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <meta name="description" content="Request new password to be send to email">
    <title>Request new password</title>

    <!--[if lt IE 9]>
	<script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
    <![endif]-->

    <link href="css/bootstrap.min.css" rel="stylesheet">
    <link href="css/font-awesome.min.css" rel="stylesheet">
    <link href="css/bootswatch.css" rel="stylesheet">

    <!-- HTML5 shim, for IE6-8 support of HTML5 elements -->
    <!--[if lt IE 9]>
        <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
    <![endif]-->
</head>
<body class="index" id="top">
    <div class="container">
        <div class="jumbotron jumbotron-fluid">
            <div class="container text-center">
                <h1 class="display-4">FIONA Data Portal</h2>
                <p class="lead">Request a new password to be sent to your institutional email address.</p>
		<hr class="my-4">
		<p>After you receive the email, try to login again <a href="/applications/User/login.php">here</a> using the temporary password from the email.</p>
            </div>
        </div>
        <div class="row justify-content-center">
	     <div class="col-md-5 col-sm-12">
            <form action="newPassword.php" method="post" id="request-password">
                <div class="form-group">
                    <input type="email" name="email" placeholder="institutional email address" class="form-control" autofocus required>
	        </div>
	        <div class="form-group">
	            <button class="btn btn-dark" type="submit">Submit</button>
	        </div>
            </form>
	    </div>
        </div>

        <div class="row justify-content-center">
	    <div style="display: none" class="alert alert-dark col-md-4 text-center" role="alert">
                <span id="message"></span>
            </div>
	</div>
	<hr/>
    </div>
    
    <script src="/js/jquery-2.1.4.min.js"></script>
    <script src="js/jquery-ui.min.js"></script>
    <!-- create an md5sum version of the password before sending it -->
    <script src="/js/md5-min.js"></script>
    <script type="text/javascript">
        jQuery(document).ready(function() {
	    if (messageTxt !== '') {
	    	jQuery('.alert').show();
                jQuery('#message').html(messageTxt);
	    }
        });
    </script>
</body>
</html>

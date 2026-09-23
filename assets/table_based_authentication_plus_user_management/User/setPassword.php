<?php
session_start();
include("../../php/AC.php");

$uuid = $_GET['uuid'] ?? null;

if (isset($_POST["password"]) && isset($_POST['uuid'])) {
   // Check if this user exists
   $password = $_POST['password'];
   $uuid = $_POST['uuid'];
   $userInfo = getUserInfoFromUUID($uuid);

   if ($userInfo == "unknown") {
      audit( "setPassword", "account does not exist. UUID: ".$uuid );
      echo "<script>alert('This account does not exist.');</script>";
   } else {
      $name = $userInfo['name'];
      $email = $userInfo['email'];

      // create a new password for this account and send to user
      audit( "setPassword", "set new password for ".$name );
      $md5version = md5($password);
      if (changePassword( $name, $md5version ) && removeUUID($name)) {
          header('Location: https://fiona.ihelse.net/applications/User/index.php');
      } else {
          echo "<script>alert('Error occured while creating password. Please contact hauke.bartsch@helse-bergen.no or zhanbolat.satybaldinov@helse-bergen.no');</script>";
          //alert('Error occured while creating password. Please contact hauke.bartsch@helse-bergen.no or zhanbolat.satybaldinov@helse-bergen.no');
      }
   }
}

if ($uuid === null) {
   header('Location: https://fiona.ihelse.net/404');
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
        <h1 class="display-4">Data Portal</h1>
        <p class="lead">Set a new password to be send to your email address.</p>
      </div>
    </div>
    <div class="row justify-content-center">
      <form class="" action="setPassword.php" method="post" id="set-password">
        <div class="form-group">
          <input type="password" name="password" placeholder="New password" class="span3" required autofocus />
        </div>
        <div class="form-group">
          <input type="password" name="password-confirm" placeholder="Repeat password" class="span3" required />
        </div>
        <p id="mismatch-error" class="text-danger" style="display: none">Passwords mismatch! Please try again.</p>
        <input type="hidden" name="uuid" value="<?= $uuid ?>" />      
        <button class="btn btn-dark" type="submit">Submit form</button>
      </form>
    </div>
  </div>
  
  <script src="/js/jquery-2.1.4.min.js"></script>
  <script src="js/jquery-ui.min.js"></script>
  <!-- create an md5sum version of the password before sending it -->
  <script src="/js/md5-min.js"></script>
  <script type="text/javascript">
     jQuery(document).ready(function() {
//        jQuery('#message').html(message);

        $("#set-password").on("submit", function(e) {
	  if ($('input[name="password"]').val() !== $('input[name="password-confirm"]').val()) {
	    e.preventDefault();
	    $("#mismatch-error").show();
          }
        });
     });
  </script>
</body>
</html>

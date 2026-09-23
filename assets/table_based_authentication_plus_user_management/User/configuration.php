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

?>
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<meta name="description" content="FIONA - Upload data portal.">
	<title>FIONA Configuration Screen</title>

	<!--[if lt IE 9]>
		<script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
	<![endif]-->


        <link href="css/select2.min.css" rel="stylesheet">
        <link href="css/bootstrap.min.css" rel="stylesheet">
	<link href="css/font-awesome.min.css" rel="stylesheet">
        <!-- HTML5 shim, for IE6-8 support of HTML5 elements -->
        <!--[if lt IE 9]>
          <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
        <![endif]-->
</head>
<body class="index">

<header>
  <div class="collapse bg-dark" id="navbarHeader">
    <div class="container-fluid">
      <div class="row">
        <div class="col-sm-8 col-md-7 py-4">
          <h4 class="text-white">About</h4>
          <p class="text-muted">This application allows the admin to configure FIONA.</p>
        </div>
        <div class="col-sm-4 offset-md-1 py-4">
          <h4 class="text-white">Contact</h4>
          <ul style="list-style-type: none;">
            <li style="background-color: transparent;" class="text-muted">Email <a href="mailto:Hauke.Bartsch@helse-bergen.no" class="text-white">Hauke Bartsch</a></li>
          </ul>
        </div>
        <div class="col-sm-8 col-md-7 py-4" style="padding-top: 0px !important;">
          <h4 class="text-white">Admin interface</h4>
	  <p class="text-muted">Change the configuration for FIONA.</p>
	</div>
      </div>
    </div>
  </div>
  <div class="navbar navbar-dark bg-dark shadow-sm">
    <div class="container-fluid d-flex justify-content-between">
      <a href="#" class="navbar-brand d-flex align-items-center">
        FIONA Configuration
      </a>
      <a href="/index.php" class="d-flex align-items-center nav-link navbar-text" title="Back to Steve">
        Steve
      </a>
      <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarHeader" aria-controls="navbarHeader" aria-expanded="false" aria-label="Toggle navigation">
        <span class="navbar-toggler-icon"></span>
      </button>
    </div>
  </div>
</header>

<div class="container">
     <div class="row">
     	  <div class="col-md">
	    <h4 style="margin-top: 20px;">Configuration options</h4>
	    <div id="result"></div>
	  </div>
     </div>
</div>
<script src="js/jquery-3.7.1.min.js"></script>
<script src="js/select2.min.js"></script>
<script type="text/javascript" src="js/tmpl.min.js"></script>
<script type="text/javascript" src="/js/md5-min.js"></script>
<script src="js/bootstrap.min.js"></script>
<script src="js/configuration.js"></script>
</body>
</html>
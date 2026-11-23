<?php
// login_clara.php: FIONA sign-in through CLARA (Sequence K,
// Clara/Design/Authentication_Authorization_Design.md §2.13). Lives next to login.php
// (applications/User/) and uses php/AC_clara.php; the settings are in the "Clara" -> "SSO"
// section of /data/config/config.json (see the header of AC_clara.php).
//
//   login_clara.php?url=/page.php    sends the browser to CLARA (with "source" set, straight
//                                    to that source, e.g. PingFederate); a user already signed
//                                    in to CLARA comes back at once
//   login_clara.php?code=...&state=  CLARA's answer: the code is redeemed by this server, the
//                                    CLARA address is mapped to the FIONA account with that
//                                    e-mail, and the FIONA session is made
//   login_clara.php?signout=1        FIONA's logout: ends the FIONA session and sends the
//                                    browser to CLARA's sign-out
//   login_clara.php?signed_out=1     where CLARA returns after its sign-out
//                                    (SSO_CLIENT_n_LOGOUT_URI)
if (session_status() == PHP_SESSION_NONE) {
   session_start();
}
include("../../php/AC_clara.php");

function h( $s ) {
   return htmlspecialchars((string)$s, ENT_QUOTES, 'UTF-8');
}

$message = "";   // shown on the page (already HTML-escaped)
$title = "Sign in with CLARA";
$show_retry = false;
$show_clara_signout = false;
$return_url = clara_sso_safe_return($_GET['url'] ?? "/index.php");
$retry_href = "login_clara.php?url=".rawurlencode($return_url);

if (clara_sso_enabled() && !clara_sso_on_canonical_host() && !isset($_GET['hostcheck'])) {
   // Opened on another host name or port than the registered return address
   // (e.g. 127.0.0.1 instead of localhost): continue on the registered one first, or
   // FIONA's session would be lost on the way back from CLARA. "hostcheck" stops a loop
   // when a proxy changes the Host header.
   $uri = isset($_SERVER['REQUEST_URI']) ? (string)$_SERVER['REQUEST_URI'] : clara_sso_login_page();
   if ($uri == "" || $uri[0] != "/" || substr($uri, 0, 2) == "//") {
      $uri = clara_sso_login_page();
   }
   header("Location: ".clara_sso_canonical_origin().$uri.(strpos($uri, "?") === FALSE ? "?" : "&")."hostcheck=1");
   exit();
}

if (!clara_sso_enabled()) {
   $message = "Sign-in with CLARA is not configured on this FIONA (section \"Clara\" &rarr; \"SSO\" in config.json).";
} else if (!clara_sso_on_canonical_host()) {
   // redirected once already and still not on the registered host: the web server or a
   // proxy in front of it passes another Host header
   $title = "Wrong address";
   $message = "Sign-in with CLARA works only when FIONA is opened at <a href=\"".h(clara_sso_canonical_origin().clara_sso_login_page())."\">"
            . h(clara_sso_canonical_origin())."</a>. This page was reached as <b>".h($_SERVER['HTTP_HOST'] ?? '')."</b>. "
            . "If you used that address, the web server or a proxy changes the host name (check \"redirect_uri\" in config.json).";
} else if (isset($_GET['signout']) || (isset($_SESSION['clara_sso']) && !isset($_SESSION['logged']))) {
   // FIONA's logout (php/logout.php only removes "logged"), or a direct sign-out request:
   // end the FIONA side, then let CLARA end its sign-in (CLARA asks the user first).
   $had_grant = isset($_SESSION['clara_sso']);
   if ($had_grant) {
      audit("login_clara", "FIONA logout of ".$_SESSION['clara_sso']['email'].", signing out of CLARA", "INFO");
   }
   clara_sso_end_local();
   unset($_SESSION['clara_sso_tx']);
   if ($had_grant) {
      header("Location: ".clara_sso_signout_url());
      exit();
   }
   $title = "Signed out";
   $message = "You are signed out of FIONA.";
   $show_retry = true;
   $retry_href = "login_clara.php";
} else if (isset($_GET['signed_out'])) {
   $title = "Signed out";
   $message = "You are signed out of FIONA.";
   $show_retry = true;
   $retry_href = "login_clara.php";
} else if (isset($_GET['state']) && (isset($_GET['code']) || isset($_GET['error']))) {
   // CLARA's answer to /sso/authorize
   if (isset($_GET['error'])) {
      unset($_SESSION['clara_sso_tx']);
      audit("login_clara", "CLARA answered ".substr((string)$_GET['error'], 0, 64), "FAILED");
      $message = "CLARA did not sign you in (".h(substr((string)$_GET['error'], 0, 64)).").";
      $show_retry = true;
   } else {
      $tx_return = $_SESSION['clara_sso_tx']['return'] ?? "/index.php";
      $result = clara_sso_finish($_GET['code'], $_GET['state']);
      if (is_string($result)) {
         header("Location: ".$result);
         exit();
      }
      $retry_href = "login_clara.php?url=".rawurlencode($tx_return);
      $show_retry = true;
      switch ($result['error']) {
         case 'no_fiona_account':
            $message = "CLARA signed you in as <b>".h($result['email'])."</b>, but FIONA has no account with this e-mail address. "
                     . "Ask a FIONA administrator to add this address to your FIONA account.";
            $show_retry = false;
            $show_clara_signout = true;
            break;
         case 'state_mismatch':
         case 'expired':
         case 'no_code':
            $message = "The sign-in took too long or was started in another browser tab. Please try again.";
            break;
         case 'clara_unreachable':
            $message = "FIONA could not reach CLARA to finish the sign-in. Please try again later.";
            break;
         default:
            $message = "CLARA refused the sign-in (".h($result['error'])."). Please try again.";
      }
   }
} else if (check_logged_only() !== FALSE) {
   // signed in already (through CLARA or with a FIONA password)
   header("Location: ".$return_url);
   exit();
} else {
   header("Location: ".clara_sso_begin($return_url));
   exit();
}
?>
<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<title>FIONA: <?php echo h($title); ?></title>
	<link href="css/bootstrap.css" rel="stylesheet">
	<link href="css/bootstrap-responsive.min.css" rel="stylesheet">
	<link href="css/font-awesome.min.css" rel="stylesheet">
	<link href="css/bootswatch.css" rel="stylesheet">
</head>
<body class="index" id="top">
  <div class="container">
   <div class="row">
     <div class="hero-unit">
       <h1><?php echo h($title); ?></h1>
       <p class="lead"><center>A service provided by the Mohn Medical Imaging and Visualization Centre of Bergen, Norway</center></p>
     </div>
   </div>
   <div class="row">
    <div class="span3"> </div>
    <div class="span6">
      <p><?php echo $message; ?></p>
      <p>
<?php if ($show_retry && clara_sso_enabled()): ?>
        <a class="btn btn-primary" href="<?php echo h($retry_href); ?>">Sign in with CLARA</a>
<?php endif; ?>
<?php if ($show_clara_signout): ?>
        <a class="btn" href="<?php echo h(clara_sso_signout_url()); ?>">Sign out of CLARA</a>
<?php endif; ?>
      </p>
      <p><a href="login.php?url=<?php echo h(rawurlencode($return_url)); ?>">Sign in with a FIONA password instead</a></p>
    </div>
   </div>
  </div>
</body>
</html>

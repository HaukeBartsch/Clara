<?php
// get/set the configuration of FIONA
date_default_timezone_set('Europe/Berlin');

session_start(); /// initialize session
include("../../../php/AC.php");
$user_name = check_logged(); /// function checks if visitor is logged.
if (!check_role("admin")) {
   echo(json_encode(array('message' => "Error: only admin user can configure.")));
   exit(0);
}

$action = "get";
if (isset($_GET['action'])) {
   $action = $_GET['action'];
}

if ($action == "get") {
   $config_file = "/data/config/config.json";
   if (file_exists($config_file)) {
     $config = json_decode(file_get_contents($config_file), TRUE);
     // some things we do not want to show
     unset($config["PROJECTTOKEN"]);
     foreach($config["Authentication"]["LDAP"] as &$entry) {
       unset($entry["password"]);
     }
     echo(json_encode($config));
     exit(0);
   } else {
     echo(json_encode(array('message' => "Error: no configuration file found.")));
   }
}

?>
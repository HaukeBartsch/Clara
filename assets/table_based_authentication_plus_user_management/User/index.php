<?php
if (session_status() == PHP_SESSION_NONE) {
   session_start();
}
include("../../php/AC.php");
$user_name = check_logged(); /// function checks if visitor is logged.
echo('<script type="text/javascript"> user_name = "'.$user_name.'"; </script>'."\n");
$permissions = list_permissions_for_user($user_name);
echo('<script type="text/javascript"> permissions = '.json_encode($permissions).'; </script>'."\n");
$user_info = getUserInfo($user_name);

$logged_in = false;
//$user_name = check_logged(); /// function checks if visitor is logged.
if (isset($_SESSION["logged"]) && array_key_exists($_SESSION["logged"],$USERS)) {
   $user_name = check_logged();
   $logged_in = true;
}

?>

<!DOCTYPE html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1.0">
	<meta name="description" content="User page">
	<title>Fiona Project User Page</title>

	<!--[if lt IE 9]>
		<script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
	<![endif]-->
	<link href="css/font-awesome.min.css" rel="stylesheet">

	<link href="css/bootstrap.min.css" rel="stylesheet">
        <!-- HTML5 shim, for IE6-8 support of HTML5 elements -->
        <!--[if lt IE 9]>
          <script src="http://html5shim.googlecode.com/svn/trunk/html5.js"></script>
        <![endif]-->
	<link href="css/style.css" rel="stylesheet">

</head>
<body class="index" id="top">
    <nav class="navbar navbar-dark bg-dark navbar-expand-lg">
	<a class="navbar-brand" href="#">
	  <svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" class="d-block mx-auto"><circle cx="12" cy="12" r="10"></circle></svg>
	</a>
      <button class="navbar-toggler" type="button" data-toggle="collapse" data-target="#navbarNavDropdown" aria-controls="navbarNavDropdown" aria-expanded="false" aria-label="Toggle navigation">
	<span class="navbar-toggler-icon"></span>
      </button>
      <div class="collapse navbar-collapse" id="navbarNavDropdown">
	<ul class="navbar-nav nav-fill w-100">
	  <li class="nav-item active">
	    <a class="nav-link" href="/" title="Fiona home page">Home</a>
	  </li>
	  <li class="nav-item">
	    <a class="nav-link" href="../../applications/Features" title="What are some key features">Features</a>
	  </li>
	  <li class="nav-item dropdown">
            <a class="nav-link dropdown-toggle" href="#" id="navbarDropdownMenuLink" role="button" data-toggle="dropdown" aria-haspopup="true" aria-expanded="false">
              Apply
            </a>
            <div class="dropdown-menu" aria-labelledby="navbarDropdownMenuLink">
	      <a class="dropdown-item" href="../../applications/Apply" title="Apply for a new project">Apply for a new research project</a>
	      <a class="dropdown-item" href="../../applications/Apply/access_exists.php" title="Apply for access to an existing project">Apply for access to an existing project</a>
            </div>
	  </li>
	<!--  <li class="nav-item">
	    <a class="nav-link" href="#" data-target="#support-modal" data-toggle="modal">Support</a>
	  </li> -->
	 <!-- <li class="nav-item">
	    <a class="nav-link" href="#" data-toggle="modal" data-target="#news-modal" >News</a>
	  </li> -->
	  <li class="nav-item">
	    <a class="nav-link" href="../../applications/About">About</a>
	  </li>
<?php if ($logged_in) : ?>
	  <li class="nav-item dropdown">
 	    <a class="nav-link dropdown-toggle" href="#" role="button" data-toggle="dropdown" aria-hasopopup="false" aria-expanded="false"><?php echo($user_name); ?></a>
            <div class="dropdown-menu" aria-labelledby="navbarDropdownMenuLink">
	      <a class="dropdown-item" href="#" title="Logout" data-toggle="modal" data-target="#change-password-box">Change password</a>
	      <a class="dropdown-item" href="index.php" title="Profile"">Profile</a>
	      <a class="dropdown-item" href="#" title="Logout" onclick="logout();">Logout</a>
            </div>	    
	  </li>
<?php endif; ?>
	</ul>
      </div>
      
    </nav>
     <div class="jumbotron jumbotron-fluid">
       <div class="container">
         <h1>Fiona Project User Page</h2>
         <p class="lead">
	   A service provided by the Mohn Medical Imaging and Visualization Center of Bergen, Norway
         </p>
      </div>
     </div>
  <div class="container">
   <div class="row">
     <div class="col-sm">
       <h4>Policy Analyzer</h4>
       <p>The current user <i><span id="user-name"><?php echo($user_name); ?></span></i> is logged in.</p>
       <p>Email: <i><span id="user-email"><?php echo($user_name); ?></span></i>.</p>
       <p>List of permissions for this user: <i><?php echo(implode(", ",$permissions)) ?></i>.</p>
     </div>
   </div>
     <hr/>

   <div class="row">
     <div class="col-sm">
       <h4>Change password</h4>
       <form>
       <div class="form-group">
           <label for="password-field1">Enter new password</label>
           <input class="form-control" type="password" id="password-field1" placeholder="*******" autofocus>
       </div>
       <div class="form-group">
           <label for="password-field2">Enter password again</label>
           <input class="form-control" type="password" id="password-field2" placeholder="">
       </div>
       <button type="button" class="btn btn-primary" onclick="changePassword();">Submit</button>

       </form>
     </div>
   </div>

   <div class="row">
     <div class="col-sm">
       <h4>Change email</h4>
       <form>
       <div class="form-group">
           <label for="password-field1">Enter new email (<span class="email"></span>)</label>
           <input class="form-control" type="email" id="email-field" autofocus>
       </div>
       <button type="button" class="btn btn-primary" onclick="changeEmail();">Submit</button>
       </form>
     </div>
   </div>


<hr/>

   <div class="row">
     <div class="col-sm">
       <h4 id="title-processing-token">Create a processing token</h4>
       <p>You may create a single token for the 'ror' tool to be able to upload workflows to Fiona. The token will only be displayed once! Provide the token to ror with 'ror config --token <token>'. If a token already exists and you create a new token the old token will no longer be valid.
       </p>
       <div class="form-group">
      	 <label for="project-selection">Select the project for your token</label>
<select class="form-select form-control" aria-label="Select your project" id="project-selection">
  <option selected>Select one of your projects</option>
</select>       
    	 <small id="projectHelp" class="form-text text-muted">Your token will only be valid for this project.</small>
       </div>
       <div class="form-group">
      	 <label for="token-id">Token</label>
     	 <input type="text" class="form-control" id="token-id" aria-describedby="tokenHelp" readonly>
    	 <small id="tokenHelp" class="form-text text-muted">Do not share your token!</small>
       </div>
       <button class="btn btn-primary" id="create-token">Create a new token</button>
     </div>
   </div>
   <hr/>

   <div class="row mb-5">

     <div class="col-12">
       <h4>Notification emails</h4>
     </div>
     
     <div class="col-sm">

       <p>You may sign up for incoming data events by choosing the project and notification frequency. An email will be sent with the report information about all the incoming events for a project.
          The report will be forwarded to your email address (<em><span id="user-email" class="email"></span></em>). To stop these emails, remove the task from the "Active task list" by clicking "x" in the upper-right corner.</p>

       <form id="events-signup-form">

         <div class="form-group">
           <label for="signup-action"><strong>Select action</strong></label>
           <select class="form-select form-control" aria-label="Select action" id="signup-action">
             <option value="" selected>Select...</option>
           </select>      
           <p class="text-danger" id="signup-action-error"></p> 
         </div>

         <div class="form-group">
           <label for="signup-project"><strong>Select project</strong></label>
           <select class="form-select form-control" aria-label="Select the project" id="signup-project">
             <option selected>Select...</option>
           </select>      
           <p class="text-danger" id="signup-project-error"></p> 
         </div>
     
         <div class="form-group">
            <label for="signup-event"><strong>Select event</strong></label>
            <select class="form-select form-control" aria-label="Select event name" id="signup-event">
              <option value="" selected>Select...</option>
            </select>
            <p class="text-danger" id="signup-event-error"></p> 
         </div>

         <div class="form-group">
            <label for="event-parameter"><strong>Select event parameter</strong></label>
            <select class="form-select form-control" aria-label="Select event parameter" id="event-parameter">
              <option value="" selected>Select...</option>
            </select>
            <p class="text-danger" id="event-parameter-error"></p> 
         </div>
     
         <button class="btn btn-primary" id="events-signup">Sign up</button>
       </form>
     </div>

     <div class="col-sm">
         <p><strong>Active task list</strong> (<span id="tasks-count"></span>)</p>
	 <hr>
         <div id="myevents" class="d-flex flex-column"></div>
     </div>
     
   </div>
  <hr>

  </div>

  </div>

      <!-- waiting wheel -->
    <div>
      <section class="wrapper dark" id="waiting_wheel" style="display: none; z-index: 1000;">
        <div class="spinner">
          <i></i>
          <i></i>
          <i></i>
          <i></i>
          <i></i>
          <i></i>
          <i></i>
        </div>
      </section>
    </div>

  <script src="js/jquery-3.6.1.min.js"></script>
  <!-- <script src="js/jquery-ui.min.js"></script> -->
  <!-- create an md5sum version of the password before sending it -->
  <script src="/js/md5-min.js"></script>
  <script src="js/popper.min.js"></script>
  <script src="js/bootstrap.min.js"></script>
  <script src="js/select2.min.js"></script>
  <script src="js/all.js"></script>

  <script type="text/javascript">

      // change the current user's password
      function changePassword() {
        var password = jQuery('#password-field1').val();
        var password2 = jQuery('#password-field2').val();
        if (password == "") {
          alert("Error: Password cannot be empty.");
          return; // no empty passwords
        }
        hash = hex_md5(password);
        hash2 = hex_md5(password2);
        if (hash !== hash2) {
          alert("Error: The two passwords are not the same, please type again.");
          return; // do nothing
        }
        jQuery.getJSON('/php/getUser.php?action=changePassword&value=' + user_name + '&value2=' + hash, function(data) {
	    alert(data.message);
        });
      }

      function changeEmail() {
          var email = jQuery('#email-field').val();
          console.log('Test' + email);
          if (email == '') {
              alert('Error: email cannot be empty');
              return;
          }
          
          jQuery.getJSON('/php/getUser.php?action=changeEmail&value=' + user_name + '&value2=' + email, function(data) {
              alert(data.message);
          });
      }

jQuery(document).ready(function() {
  // for all projects that we have access to
  for(var i = 0; i < permissions.length; i++) {
     var t = permissions[i];
     var prefix = "Project";
     if (t.slice(0,prefix.length) == prefix) {
        t = t.slice(prefix.length);
	jQuery("#project-selection").append("<option value=" + t + ">" + t + "</option>");
     }
  }

  jQuery('#create-token').on('click', function() {
     var project = jQuery("#project-selection").val();
     jQuery.getJSON('tokenStorage.php', { project: project }, function(data) {
        if (typeof(data['token']) != 'undefined') {
	   jQuery('#token-id').val(data['token']);
	} else {
	   alert("Error: could not receive the token from the server. ".JSON.stringify($data));
	}
     });
  });

  jQuery.getJSON('php/getProjects.php', {}, function(data) {
      jQuery('#signup-project').children().remove();
      jQuery('#signup-project').append("<option value='' selected>Select...</option>");
	for (var i = 0; i < data.length; i++) {
	    jQuery('#signup-project').append('<option value="' + data[i]['record_id'] + '">' + data[i]['record_id'] + "</option>");
	}
        jQuery('#signup-project-selection').select2({ minimumResultsForSearch: 10 });	
    }).fail(function() {

    });
  
  let eventsData = null;
  jQuery.getJSON('asttt/code/php/getEvents.php', {}, function(data) {
      // imported from all.js
      eventsData = data;
      setEventNames(data);
  });

  jQuery("#signup-event").on("change", function() {
      // imported from all.js
      const selectedTxt = jQuery("#signup-event option:selected").text();
      jQuery("#event-parameter").find("option").remove();
      jQuery("#event-parameter").append(`<option value="" selected>Select...</option>`);
      setEventParams(selectedTxt, eventsData);      
  });

  jQuery.getJSON('asttt/code/php/getActions.php', {}, function(data) {
      // imported from all.js
      setActions(data);
  });

  jQuery(document).on('click', '.test-event', function() {
      jQuery('#waiting_wheel').fadeIn();
      const linkId = $(this).data('id');
      $.ajax({
          url: "asttt/code/php/testEvent.php",
	  data: {linkId: linkId},
      })
          .done(function (msg) {
              jQuery('#waiting_wheel').fadeOut();
	      alert("Action was successfully executed.");
          })
	  .fail(function (msg) {
              jQuery('#waiting_wheel').fadeOut();
	      alert("Error. No action was executed.");
	      console.log(msg);
	  });
  });

});

  </script>

</body>
</html>

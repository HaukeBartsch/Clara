// Find user's email and change elements text
function getUserEmail() {
    const userName = $("#user-name").html();

    $.getJSON("asttt/code/php/getUserData.php", function(data) {
	Object.values(data["users"]).forEach(function(obj) {
	    if (obj["name"] === userName) {
		$("#user-email").text(obj["email"]);
		jQuery('.email').text(obj["email"]);
	    }
	});
    });
}

      // logout the current user
      function logout() {
        jQuery.get('/php/logout.php', function(data) {
          if (data == "success") {
            // user is logged out, reload this page
            location.reload();
          } else {
            alert('something went terribly wrong during logout: ' + data);
          }
        });
      }

const dayParams = {
    0: "05:00",
    1: "06:00",
    2: "07:00",
    3: "08:00",
    4: "09:00",
    5: "10:00",
    6: "11:00",
    7: "12:00",
    8: "13:00",
    9: "14:00",
    10: "15:00",
    11: "16:00",
    12: "17:00",
    13: "18:00",
    14: "19:00",
    15: "20:00",
    16: "21:00",
    17: "22:00"
}

const monthParams = {
    0: "1st of month",
    1: "mid of month",
    2: "end of month"
}

const weekParams = {
    0: "Sunday",
    1: "Monday",
    2: "Tuesday",
    3: "Wednesday",
    4: "Thursday",
    5: "Friday",
    6: "Sunday"
}

// Fill in events cards (and get all actions at the same time)
function readEvents() {
    const userName = $("#user-name").html();
    
    $.getJSON("asttt/code/php/getUserEvents.php", function(data) {
	$("#myevents").empty();

	if (!data) {
	    return;
	}

	let counter = 0;
	data.forEach(function(eventInfo) {
	    let eventParam = '';

	    if (eventInfo.event.toLowerCase().includes('daily') === true) {
		eventParam = dayParams[eventInfo.event_param.TimePoint];
	    }

	    if (eventInfo.event.toLowerCase().includes('week') === true) {
		eventParam = weekParams[eventInfo.event_param.TimePoint];
	    }

	    if (eventInfo.event.toLowerCase().includes('month') === true) {
		eventParam = monthParams[eventInfo.event_param.TimePoint];
	    }
	    
	    if (eventInfo.user === userName) {
                $("#myevents").append(`<div 
                                         class="d-flex justify-content-between mb-2 user-event" 
                                         data-user="${eventInfo.user}" 
                                         data-project="${eventInfo.action_param.Project}"
                                         data-id="${eventInfo.id}"
                                       >
                                         <div class="p2 d-flex bordered-task">
                                           <p style="margin-bottom: 0; margin-right: 5px"><em>${eventInfo.action_param.Project} - ${eventInfo.event} ${eventParam} - ${eventInfo.action}</em></p>
                                         </div>
                                         <button data-id="${eventInfo.id}" class="btn btn-success btn-sm test-event ml-auto p-2" style="margin-right: 4px" title="Trigger the report right now, only once.">Test</button>
                                         <button class="btn btn-outline-danger btn-sm remove-event" title="Remove the report from your list of active reports">X</button>
                                       </div>`);
		counter++;
	    }

	    $('#tasks-count').html(counter);
	});	
    });
}

// Remove event from user list
function removeEvent() {
    $(document).on("click", ".remove-event", function() {
        const project = $(this).parent().data("project");
	const user = $(this).parent().data("user");
	const parentElem = $(this).parent();

	$.ajax({
            url: "asttt/code/php/removeEvent.php",
	    data: {user: user, project: project},
	})
	    .done(function (msg) {
	        parentElem.remove();
		$('#tasks-count').html($(".user-event").length);
		alert("Event was removed from the list.");
	    })
	    .fail(function (msg) {
		console.log(msg);
	    });
    });
}

// Process adding event based on data collected from form
function processSignUp() {
    $("#events-signup-form").submit(function(e) {
	e.preventDefault();
	const action = $('#signup-action').val();
	const project = $('#signup-project').val();
        const event = $('#signup-event').val();
	const eventParam = $('#event-parameter').val();
	const user = $('#user-name').text();
	let isError = false;

	if (!action) {
	    $('#signup-action-error').html('Action must be selected.');
	    isError = true;
	}

	if (!project) {
	    $('#signup-project-error').html('Project must be selected.');
	    isError = true;
	}

	if (!event) {
	    $('#signup-event-error').html('Event must be selected.');
	    isError = true;
	}

	if (!eventParam) {
	    $('#event-parameter-error').html('Event parameter must be selected.');
	    isError = true;
	}

	if (isError) {
	    return;
	}

	$.ajax({
            url: "asttt/code/php/addEvent.php",
	    data: {action: action,
		   project: project,
		   event: event,
		   eventParam: eventParam,
		   user: user}
	})
	    .done(function(data) {
		const response = JSON.parse(data);
		
                if (response.success) {
		    readEvents();
		    console.log(response.message);
		    alert("Event was added to the list");
		} else {
		    console.log(response.message);
		    alert(response.message);
		}
	    })
	    .fail(function(data) {
                console.log("Error. Can't process ajax request.");
	    });
	;
    });
}

// Track changes in notifications signup form and remove validation error messages
function processSignUpErrors() {
    $("#signup-project-selection").on('change', function(e) {
        $("#signup-project-error").empty();
    });
    
    $("#notification-frequency-selection").on('change', function(e) {
	$('#signup-notification-error').empty();
    });
}

$(document).ready(function() {
    getUserEmail();
    readEvents();
    removeEvent();
    processSignUp();
    processSignUpErrors();
});

// Set events for the form 
function setEventNames(data) {
    data[0].forEach(function(elem, index) {
	const eventName = elem.name;
	const eventParams = elem.parameter;

	$("#signup-event").append(`<option value="${eventName}">${eventName}</option>`);
    });
}

// Set event parameters for the form based on the event that was chosen
function setEventParams(eventName, data) {
    data[0].forEach(function(elem, index) {
	if (elem.name === eventName) {
	    elem.parameter.TimePoint.forEach(function(elem, index) {
                $("#event-parameter").append(`<option value="${index}">${elem}</option>`);
	    });
	}
    });
}

// Set actions for the form
function setActions(data) {
    data.forEach(function(elem, index) {
	$("#signup-action").append(`<option value="${elem.name}">${elem.name}</option>`);
    });
}

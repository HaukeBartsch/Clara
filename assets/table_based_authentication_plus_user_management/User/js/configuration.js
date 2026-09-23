jQuery(document).ready(function() {
    console.log("in ready");

    jQuery.getJSON("php/getConfig.php", { 'action': 'get' }, function(data) {
	var keys = Object.keys(data);
	var counter = 0;
	for (var i = 0; i < keys.length; i++) {
	    if (typeof data[keys[i]] === 'object') {		
		var keys2 = Object.keys(data[keys[i]]);
		jQuery("#result").append("<hr>");
		for (var j = 0; j < keys2.length; j++) {
		    var keys3 = Object.keys(data[keys[i]][keys2[j]]);
		    for (var k = 0; k < keys3.length; k++) {
			if (typeof data[keys[i]][keys2[j]][keys3[k]] === 'object') {
			    jQuery('#result').append("<hr>");
			    var keys4 = Object.keys(data[keys[i]][keys2[j]][keys3[k]]);
			    for (var l = 0; l < keys4.length; l++) {
				var pos = "k1='" + keys[i] + "' k2='" + keys2[j] + "' k3='" + keys3[k] + "' k4='" + keys4[l] + "'";
				jQuery("#result").append("<div class='row' style='margin-bottom: 10px;'><label for='entry-" + counter + "' class='col-sm-4 col-form-label'>" + keys2[j] + " - " + keys3[k] + " - " + keys4[l] + "</label><div class=\"col-sm-8\">" + "<input id='entry-" + counter + "'" + pos + " class='form-control' type='text' value='" + data[keys[i]][keys2[j]][keys3[k]][keys4[l]] + "'></div></div>");
				counter++;
			    }			    
			} else {
			    var pos = "k1='" + keys[i] + "' k2='" + keys2[j] + "' k3='" + keys3[k] + "'";
			    jQuery("#result").append("<div class='row' style='margin-bottom: 10px;'><label for='entry-" + counter + "' class='col-sm-3 col-form-label'>" + keys2[j] + " - " + keys3[k] + "</label><div class=\"col-sm-9\">" + "<input id='entry-" + counter + "'" + pos + " class='form-control' type='text' value='" + data[keys[i]][keys2[j]][keys3[k]] + "'></div></div>");
			    counter++;
			}
		    }
		}
		continue;
	    }
	    var pos = "k1='" + keys[i] + "'";
	    jQuery("#result").append("<div class='row' style='margin-bottom: 10px;'><label for='entry-" + counter + "' class='col-sm-2 col-form-label'>" + keys[i] + "</label><div class=\"col-sm-10\">" + "<input id='entry-" + counter + "'" + pos + " class='form-control' type='text' value='" + data[keys[i]] + "'></div></div>");
	    counter++;
	}
    });
});

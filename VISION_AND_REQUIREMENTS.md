# Clara - clinical study management system

A research electronic data capture system for clinical studies (web-based platform to collect project data)
 - projects are secured with tokens that map to user accounts
 - a user account has permissions (authorization) and authenticates against an OAuth2 server
 - if the OAuth2 server is unavailable or the login fails, the system falls back to up to three configured LDAP servers, queried in sequence
 - a user account may have a single token (uuid) that grants permissions to a project based on the user accounts roles and permissions 
 - permissions include "view", "change", "add", "export all", "export anonymized" for records in a project
 - a project has a unique project name, information about ethics approval (REK-number), start and end date and a list of roles and users. A project is created based on the following information:

```
agreed-to-end-user-contract
    true
ProjectName
    EMIT-23
ParticipantNames
    0001_01
EventNames
    01, 02, 03
Organization (OTHER, VEST, HBE, SUS, FOR, FON, UIB, UIS, HVL, NAT EU)
    OTHER
Name-of-PI
    Lars Akslen
Email-of-PI
    lars.akslen@uib.no
Data-Manager
    Lars Akslen
Email-of-DM
    lars.akslen@uib.no
REK
    2017/2180
REK-START
    2018-01-01
REK-END
    2043-01-01
end-provision (delete or anonymize)
    delete
option-radiology
    false
option-pathology
    false
option-pathology-type
    DICOM
option-redcap-only
    false
option-data-collection-from-home
    false
OtherUsers

```

 - a project role is a collection of permissions. Role "data-manager" has permissions to change the project structure, role "data-entry" cannot change the project structure but can enter/change records, role "controller" can see the project structure but cannot change records. Additional roles can be created with a mixture of permissions (import/export/tools).
 - the project structure is organized by (study-)arms. An arm has its own records but may share instruments with other records in the same project. An arm also has its own event list and instrument by event mapping.
 - An instrument is a collection of fields. Each field is described by a data dictionary entry with a unique name (lower-case alphanumeric with underscores, after 26 characters you get a warning), a field label (question, description) and a field type.  A field name is unique in a project. Here is an example csv file that contains a pr2mask instrument description with all fields (data dictionary format).

```
"Variable / Field Name","Form Name","Section Header","Field Type","Field Label","Choices, Calculations, OR Slider Labels","Field Note","Text Validation Type OR Show Slider Number","Text Validation Min","Text Validation Max",Identifier?,"Branching Logic (Show field only if...)","Required Field?","Custom Alignment","Question Number (surveys only)","Matrix Group Name","Matrix Ranking?","Field Annotation"
physical_size,pr2mask,,text,"Physical size",,,,,,,,,,,,,
region_number,pr2mask,,text,"Region number",,,,,,,,,,,,,
boundingbox,pr2mask,,text,"Bounding box information for the regions of interest",,,,,,,,,,,,,
principal_axes,pr2mask,,text,"Principal axes",,,,,,,,,,,,,
centroid,pr2mask,,text,Centroid,,,,,,,,,,,,,
elongation,pr2mask,,text,Elongation,,,,,,,,,,,,,
flatness,pr2mask,,text,Flatness,,,,,,,,,,,,,
roundness,pr2mask,,text,Roundness,,,,,,,,,,,,,
perimeter,pr2mask,,text,Perimeter,,,,,,,,,,,,,
feret_diameter,pr2mask,,text,"Feret diameter",,,,,,,,,,,,,
number_of_pixel,pr2mask,,text,"Number of pixel",,,,,,,,,,,,,
image_intensity_max,pr2mask,,text,"Image intensity maximum",,,,,,,,,,,,,
image_intensity_min,pr2mask,,text,"Image intensity minimum",,,,,,,,,,,,,
image_intensity_mean,pr2mask,,text,"Image intensity mean",,,,,,,,,,,,,
image_intensity_median,pr2mask,,text,"Image intensity median",,,,,,,,,,,,,
image_intensity_stdev,pr2mask,,text,"Image intensity standard deviation",,,,,,,,,,,,,
image_intensity_sum,pr2mask,,text,"Sum of image intensities",,,,,,,,,,,,,
image_intensity_q1,pr2mask,,text,"Image intensity Q1",,,,,,,,,,,,,
image_intensity_q3,pr2mask,,text,"Image intensity Q3",,,,,,,,,,,,,
equivalent_spherical_radius,pr2mask,,text,"Equivalent spherical radius",,,,,,,,,,,,,
equivalent_elliptical_radius,pr2mask,,text,"Equivalent elliptical radius",,,,,,,,,,,,,
equivalent_spherical_perimeter,pr2mask,,text,"Equivalent spherical perimeter",,,,,,,,,,,,,
equivalent_ellipsoid_diameter,pr2mask,,text,"Equivalent ellipsoid diameter",,,,,,,,,,,,,
pixel_on_border,pr2mask,,text,"Pixel on border",,,,,,,,,,,,,
principal_moments,pr2mask,,text,"Principal moments",,,,,,,,,,,,,
perimeter_on_border,pr2mask,,text,"Perimeter on border",,,,,,,,,,,,,
perimeter_on_border_ratio,pr2mask,,text,"Perimeter on border ratio",,,,,,,,,,,,,
tex_cluster_prominence_00,pr2mask,,text,"Texture cluster prominence 0",,,,,,,,,,,,,
tex_cluster_prominence_01,pr2mask,,text,"Texture cluster prominence 1",,,,,,,,,,,,,
tex_cluster_prominence_02,pr2mask,,text,"Texture cluster prominence 2",,,,,,,,,,,,,
tex_cluster_prominence_03,pr2mask,,text,"Texture cluster prominence 3",,,,,,,,,,,,,
tex_cluster_prominence_04,pr2mask,,text,"Texture cluster prominence 4",,,,,,,,,,,,,
tex_cluster_prominence_05,pr2mask,,text,"Texture cluster prominence 5",,,,,,,,,,,,,
tex_cluster_prominence_06,pr2mask,,text,"Texture cluster prominence 6",,,,,,,,,,,,,
tex_cluster_prominence_07,pr2mask,,text,"Texture cluster prominence 7",,,,,,,,,,,,,
tex_cluster_prominence_08,pr2mask,,text,"Texture cluster prominence 8",,,,,,,,,,,,,
tex_cluster_prominence_09,pr2mask,,text,"Texture cluster prominence 9",,,,,,,,,,,,,
tex_cluster_prominence_10,pr2mask,,text,"Texture cluster prominence 10",,,,,,,,,,,,,
tex_cluster_prominence_11,pr2mask,,text,"Texture cluster prominence 11",,,,,,,,,,,,,
tex_cluster_prominence_12,pr2mask,,text,"Texture cluster prominence 12",,,,,,,,,,,,,
tex_cluster_shade_00,pr2mask,,text,"Texture cluster shade 0",,,,,,,,,,,,,
tex_cluster_shade_01,pr2mask,,text,"Texture cluster shade 1",,,,,,,,,,,,,
tex_cluster_shade_02,pr2mask,,text,"Texture cluster shade 2",,,,,,,,,,,,,
tex_cluster_shade_03,pr2mask,,text,"Texture cluster shade 3",,,,,,,,,,,,,
tex_cluster_shade_04,pr2mask,,text,"Texture cluster shade 4",,,,,,,,,,,,,
tex_cluster_shade_05,pr2mask,,text,"Texture cluster shade 5",,,,,,,,,,,,,
tex_cluster_shade_06,pr2mask,,text,"Texture cluster shade 6",,,,,,,,,,,,,
tex_cluster_shade_07,pr2mask,,text,"Texture cluster shade 7",,,,,,,,,,,,,
tex_cluster_shade_08,pr2mask,,text,"Texture cluster shade 8",,,,,,,,,,,,,
tex_cluster_shade_09,pr2mask,,text,"Texture cluster shade 9",,,,,,,,,,,,,
tex_cluster_shade_10,pr2mask,,text,"Texture cluster shade 10",,,,,,,,,,,,,
tex_cluster_shade_11,pr2mask,,text,"Texture cluster shade 11",,,,,,,,,,,,,
tex_cluster_shade_12,pr2mask,,text,"Texture cluster shade 12",,,,,,,,,,,,,
tex_correlation_00,pr2mask,,text,"Texture correlation 0",,,,,,,,,,,,,
tex_correlation_01,pr2mask,,text,"Texture correlation 1",,,,,,,,,,,,,
tex_correlation_02,pr2mask,,text,"Texture correlation 2",,,,,,,,,,,,,
tex_correlation_03,pr2mask,,text,"Texture correlation 3",,,,,,,,,,,,,
tex_correlation_04,pr2mask,,text,"Texture correlation 4",,,,,,,,,,,,,
tex_correlation_05,pr2mask,,text,"Texture correlation 5",,,,,,,,,,,,,
tex_correlation_06,pr2mask,,text,"Texture correlation 6",,,,,,,,,,,,,
tex_correlation_07,pr2mask,,text,"Texture correlation 7",,,,,,,,,,,,,
tex_correlation_08,pr2mask,,text,"Texture correlation 8",,,,,,,,,,,,,
tex_correlation_09,pr2mask,,text,"Texture correlation 9",,,,,,,,,,,,,
tex_correlation_10,pr2mask,,text,"Texture correlation 10",,,,,,,,,,,,,
tex_correlation_11,pr2mask,,text,"Texture correlation 11",,,,,,,,,,,,,
tex_correlation_12,pr2mask,,text,"Texture correlation 12",,,,,,,,,,,,,
tex_energy_00,pr2mask,,text,"Texture energy 0",,,,,,,,,,,,,
tex_energy_01,pr2mask,,text,"Texture energy 1",,,,,,,,,,,,,
tex_energy_02,pr2mask,,text,"Texture energy 2",,,,,,,,,,,,,
tex_energy_03,pr2mask,,text,"Texture energy 3",,,,,,,,,,,,,
tex_energy_04,pr2mask,,text,"Texture energy 4",,,,,,,,,,,,,
tex_energy_05,pr2mask,,text,"Texture energy 5",,,,,,,,,,,,,
tex_energy_06,pr2mask,,text,"Texture energy 6",,,,,,,,,,,,,
tex_energy_07,pr2mask,,text,"Texture energy 7",,,,,,,,,,,,,
tex_energy_08,pr2mask,,text,"Texture energy 8",,,,,,,,,,,,,
tex_energy_09,pr2mask,,text,"Texture energy 9",,,,,,,,,,,,,
tex_energy_10,pr2mask,,text,"Texture energy 10",,,,,,,,,,,,,
tex_energy_11,pr2mask,,text,"Texture energy 11",,,,,,,,,,,,,
tex_energy_12,pr2mask,,text,"Texture energy 12",,,,,,,,,,,,,
tex_entropy_00,pr2mask,,text,"Texture entropy 0",,,,,,,,,,,,,
tex_entropy_01,pr2mask,,text,"Texture entropy 1",,,,,,,,,,,,,
tex_entropy_02,pr2mask,,text,"Texture entropy 2",,,,,,,,,,,,,
tex_entropy_03,pr2mask,,text,"Texture entropy 3",,,,,,,,,,,,,
tex_entropy_04,pr2mask,,text,"Texture entropy 4",,,,,,,,,,,,,
tex_entropy_05,pr2mask,,text,"Texture entropy 5",,,,,,,,,,,,,
tex_entropy_06,pr2mask,,text,"Texture entropy 6",,,,,,,,,,,,,
tex_entropy_07,pr2mask,,text,"Texture entropy 7",,,,,,,,,,,,,
tex_entropy_08,pr2mask,,text,"Texture entropy 8",,,,,,,,,,,,,
tex_entropy_09,pr2mask,,text,"Texture entropy 9",,,,,,,,,,,,,
tex_entropy_10,pr2mask,,text,"Texture entropy 10",,,,,,,,,,,,,
tex_entropy_11,pr2mask,,text,"Texture entropy 11",,,,,,,,,,,,,
tex_entropy_12,pr2mask,,text,"Texture entropy 12",,,,,,,,,,,,,
tex_haralick_correlation_00,pr2mask,,text,"Texture Haralick correlation 0",,,,,,,,,,,,,
tex_haralick_correlation_01,pr2mask,,text,"Texture Haralick correlation 1",,,,,,,,,,,,,
tex_haralick_correlation_02,pr2mask,,text,"Texture Haralick correlation 2",,,,,,,,,,,,,
tex_haralick_correlation_03,pr2mask,,text,"Texture Haralick correlation 3",,,,,,,,,,,,,
tex_haralick_correlation_04,pr2mask,,text,"Texture Haralick correlation 4",,,,,,,,,,,,,
tex_haralick_correlation_05,pr2mask,,text,"Texture Haralick correlation 5",,,,,,,,,,,,,
tex_haralick_correlation_06,pr2mask,,text,"Texture Haralick correlation 6",,,,,,,,,,,,,
tex_haralick_correlation_07,pr2mask,,text,"Texture Haralick correlation 7",,,,,,,,,,,,,
tex_haralick_correlation_08,pr2mask,,text,"Texture Haralick correlation 8",,,,,,,,,,,,,
tex_haralick_correlation_09,pr2mask,,text,"Texture Haralick correlation 9",,,,,,,,,,,,,
tex_haralick_correlation_10,pr2mask,,text,"Texture Haralick correlation 10",,,,,,,,,,,,,
tex_haralick_correlation_11,pr2mask,,text,"Texture Haralick correlation 11",,,,,,,,,,,,,
tex_haralick_correlation_12,pr2mask,,text,"Texture Haralick correlation 12",,,,,,,,,,,,,
tex_inertia_00,pr2mask,,text,"Texture inertia 0",,,,,,,,,,,,,
tex_inertia_01,pr2mask,,text,"Texture inertia 1",,,,,,,,,,,,,
tex_inertia_02,pr2mask,,text,"Texture inertia 2",,,,,,,,,,,,,
tex_inertia_03,pr2mask,,text,"Texture inertia 3",,,,,,,,,,,,,
tex_inertia_04,pr2mask,,text,"Texture inertia 4",,,,,,,,,,,,,
tex_inertia_05,pr2mask,,text,"Texture inertia 5",,,,,,,,,,,,,
tex_inertia_06,pr2mask,,text,"Texture inertia 6",,,,,,,,,,,,,
tex_inertia_07,pr2mask,,text,"Texture inertia 7",,,,,,,,,,,,,
tex_inertia_08,pr2mask,,text,"Texture inertia 8",,,,,,,,,,,,,
tex_inertia_09,pr2mask,,text,"Texture inertia 9",,,,,,,,,,,,,
tex_inertia_10,pr2mask,,text,"Texture inertia 10",,,,,,,,,,,,,
tex_inertia_11,pr2mask,,text,"Texture inertia 11",,,,,,,,,,,,,
tex_inertia_12,pr2mask,,text,"Texture inertia 12",,,,,,,,,,,,,
tex_inverse_difference_moment_00,pr2mask,,text,"Texture inverse difference moment 0",,,,,,,,,,,,,
tex_inverse_difference_moment_01,pr2mask,,text,"Texture inverse difference moment 1",,,,,,,,,,,,,
tex_inverse_difference_moment_02,pr2mask,,text,"Texture inverse difference moment 2",,,,,,,,,,,,,
tex_inverse_difference_moment_03,pr2mask,,text,"Texture inverse difference moment 3",,,,,,,,,,,,,
tex_inverse_difference_moment_04,pr2mask,,text,"Texture inverse difference moment 4",,,,,,,,,,,,,
tex_inverse_difference_moment_05,pr2mask,,text,"Texture inverse difference moment 5",,,,,,,,,,,,,
tex_inverse_difference_moment_06,pr2mask,,text,"Texture inverse difference moment 6",,,,,,,,,,,,,
tex_inverse_difference_moment_07,pr2mask,,text,"Texture inverse difference moment 7",,,,,,,,,,,,,
tex_inverse_difference_moment_08,pr2mask,,text,"Texture inverse difference moment 8",,,,,,,,,,,,,
tex_inverse_difference_moment_09,pr2mask,,text,"Texture inverse difference moment 9",,,,,,,,,,,,,
tex_inverse_difference_moment_10,pr2mask,,text,"Texture inverse difference moment 10",,,,,,,,,,,,,
tex_inverse_difference_moment_11,pr2mask,,text,"Texture inverse difference moment 11",,,,,,,,,,,,,
tex_inverse_difference_moment_12,pr2mask,,text,"Texture inverse difference moment 12",,,,,,,,,,,,,
```

 - A field can be any of the following field types:
   - text: text fields can have validations - integer (min/max), floating point, email address, medical record number (11 digits), date (Y-m-d, m-d-Y, etc.)
   - dropdown (single answer): Choices have a numeric code (value) and a label (text)
   - radio buttons: List of numeric codes and labels, choice is stored as code
   - matrix field: grouped fields under the same description and share the same field types but have different questions (labels). For example the choices might be radio buttons 1..10 in a table with first column a question (like a body part) and a general question above that asks for pain in each body part. The matrix field explains the coding for each choice only once in the table header.
   - description field (only text no values)
   - header field (only text no values for formatted differently)
 - Events are project specific time-points - identified with a label and a unique name as "baseline" and "followup" or "week52". The unique name is created from the label followed by the arm name (_arm_1, _arm_2). An event can have properties such as a period in days after the date of the first (baseline) event in a project (like 7, for seven days after baseline for this participant/record). Add additionally the ability to store information for +- days around the event (-2,+3) as a safe region for data capture for this event. If an instrument is added to an event it becomes active in the project (can be used for data entry). The instrument by event mapping table (checkboxes for each pair of instrument and event) defines the design of a projects arm.


 The preferred way to store the above information is in a few database tables (backend). Records in the data table should contain the identifiers to make a record unique: project_id, record_id, unique_event_name (includes arm information), repeating_instrument (instrument name), repeating_instance_number (numeric int starting with 1), field_name (lowercase alphanum with underscores), value (a very long string/binary value). Adding a new field to an existing project would not change the data table layout. Adding a new project would not change the data table layout. Add indices to make it fast to lookup all values for one project and all values for one record_id. It should not be possible to add in the same project, event, repeating_instrument, repeating_instance_number and field a second value.

Information about projects, arms, instruments inside projects, the full data dictionary (fields by instrument by project) should be stored in separate tables.

The above data model should be setup using SQL statements that create or alter an existing database. Support only SQL feature that exist in current versions of MariaDB.
SQLite is supported as the development database, while production runs on MariaDB. SQL statements shall target features common to both databases, with documented exceptions for MariaDB-only features (e.g. audit table partitioning).


# Endpoints used by Fiona

Use a clean-room design for the whole development and duplicate the functional interface.

Implement an API endpoint using golang and OpenAPI with Swagger that support calls from the research information system like:

```
    $data = array(
        'token' => $token_DataTransferProject,
        'content' => 'record',
        'format' => 'json',
        'type' => 'flat',
        'csvDelimiter' => '',
        'records' => array($project),
        'fields' => array('project_id'),
        'rawOrLabel' => 'raw',
        'rawOrLabelHeaders' => 'raw',
        'exportCheckboxLabel' => 'false',
        'exportSurveyFields' => 'false',
        'exportDataAccessGroups' => 'false',
        'returnFormat' => 'json'
    );

    $ch = curl_init();
    curl_setopt($ch, CURLOPT_URL, 'https://my-api-server:4444/api/');
    $token = "<a users token from the database for access, identifies the project>";
    $data = array(
        'token' => $token,
        'content' => 'record',
        'format' => 'json',
        'type' => 'flat',
        'csvDelimiter' => '',
        //'fields' => array('study_instance_uid','transfer_project_name','transfer_mapped_uid','),
        'forms' => array('transfers'),
        'rawOrLabel' => 'raw',
        'rawOrLabelHeaders' => 'raw',
        'exportCheckboxLabel' => 'false',
        'exportSurveyFields' => 'false',
        'exportDataAccessGroups' => 'false',
        'returnFormat' => 'json',
        'filterLogic' => '[transfer_mapped_uid]="'.$StudyInstanceUIDInPACS.'"'
    );
```

Return information about the project (name, description, PI, REK-number, start/end dates).

```
       $data = array(
          'token' => $token,
          'content' => 'metadata',
          'format' => 'json',
          'returnFormat' => 'json'
       );
       $ch = curl_init();
       curl_setopt($ch, CURLOPT_URL, 'https://' . $REDCAPHOSTNAME . ':4444/api/');
```

```
  $data = array(
    'token' => $erg['project_token'],
    'content' => 'event',
    'format' => 'json',
    'returnFormat' => 'json'
  );
  $ch = curl_init();
  curl_setopt($ch, CURLOPT_URL, 'https://' . $REDCAPHOSTNAME . ':4444/api/');
```

```
  $data = array(
     'token' => $token,
     'content' => 'generateNextRecordName'
  );
  $ch = curl_init();
  curl_setopt($ch, CURLOPT_URL, 'https://' . $REDCAPHOSTNAME . ':4444/api/');
```


```
    var data = {
        'token': tokens[site],
        'content': 'formEventMapping',
        'format': 'json',
        'returnFormat': 'json'
    };

    var headers = {
        'User-Agent': 'Super Agent/0.0.1',
        'Content-Type': 'application/x-www-form-urlencoded'
    }

    // The penaly to calling request is that we have to wait here for .5 second
    // This wait will ensure that we don't flood redcap and bring it down using the API.
    var waitTill = new Date(new Date().getTime() + 10 * 1000);
    while (waitTill > new Date()) { }
    // a while wait ends

    var url = "https://abcd-rc.ucsd.edu/redcap/api/";
```

```
  $data = array(
    'token' => $token,
    'content' => 'exportFieldNames',
    'format' => 'json',
    'returnFormat' => 'json'
  );
  $ch = curl_init();
  curl_setopt($ch, CURLOPT_URL, 'https://fiona.ihelse.net:4444/api/');
```

Example access to end points (cURL).

### Export Project Info

```
#!/bin/sh
DATA="token=849725F85DCB10342FED8C9AC3BF6BD6&content=project&format=json&returnFormat=json"
CURL=`which curl`
$CURL -H "Content-Type: application/x-www-form-urlencoded" \
      -H "Accept: application/json" \
      -X POST \
      -d $DATA \
      https://redcap.helse-vest.no/api/
```

generated output:

```
{"project_id":14,"project_title":"DataTransferProjects","creation_time":"2019-04-11 10:00:33","production_time":"","in_production":0,"project_language":"English","purpose":4,"purpose_other":"","project_notes":"","custom_record_label":"","secondary_unique_field":"","is_longitudinal":1,"has_repeating_instruments_or_events":1,"surveys_enabled":0,"scheduling_enabled":0,"record_autonumbering_enabled":0,"randomization_enabled":0,"ddp_enabled":0,"project_irb_number":"","project_grant_number":"","project_pi_firstname":"","project_pi_lastname":"","project_pi_email":"","display_today_now_button":1,"missing_data_codes":"","external_modules":"","bypass_branching_erase_field_prompt":0}
```

### Export Events

```
#!/bin/sh
DATA="token=921AD1F8B4ADA41EA7A05696888CC83D&content=event&format=json&returnFormat=json"
CURL=`which curl`
$CURL -H "Content-Type: application/x-www-form-urlencoded" \
      -H "Accept: application/json" \
      -X POST \
      -d $DATA \
      https://fiona.ihelse.net:4444/api/
```

generated response:

```
[{"event_name":"Event 1","arm_num":1,"unique_event_name":"event_1_arm_1","custom_event_label":null,"event_id":41},{"event_name":"baseline","arm_num":1,"unique_event_name":"baseline_arm_1","custom_event_label":null,"event_id":966},{"event_name":"M5Y1","arm_num":1,"unique_event_name":"m5y1_arm_1","custom_event_label":null,"event_id":967},{"event_name":"M12Y1","arm_num":1,"unique_event_name":"m12y1_arm_1","custom_event_label":null,"event_id":968},{"event_name":"Y2","arm_num":1,"unique_event_name":"y2_arm_1","custom_event_label":null,"event_id":969},{"event_name":"Y5","arm_num":1,"unique_event_name":"y5_arm_1","custom_event_label":null,"event_id":970},{"event_name":"Y10","arm_num":1,"unique_event_name":"y10_arm_1","custom_event_label":null,"event_id":971},{"event_name":"Y15","arm_num":1,"unique_event_name":"y15_arm_1","custom_event_label":null,"event_id":972}]
```

### Export Records

```
#!/bin/sh
DATA="token=921AD1F8B4ADA41EA7A05696888CC83D&content=record&action=export&format=json&type=flat&csvDelimiter=&rawOrLabel=raw&rawOrLabelHeaders=raw&exportCheckboxLabel=false&exportSurveyFields=false&exportDataAccessGroups=false&returnFormat=json"
CURL=`which curl`
$CURL -H "Content-Type: application/x-www-form-urlencoded" \
      -H "Accept: application/json" \
      -X POST \
      -d $DATA \
      https://fiona.ihelse.net:4444/api/
```

generated response:

```
[{"record_id":"8DISC","redcap_event_name":"event_1_arm_1","redcap_repeat_instrument":"","redcap_repeat_instance":"","leave_alone_notes":"","leave_alone_current_rek":"","leave_alone_mrn":"","leave_alone_do_not_touch_complete":"","project_name":"8DISC","project_type":"","project_organization":"","project_contact":"Ansgar Espeland","project_contact_email":"ansgar.espeland@gmail.com","project_patient_naming":"8DISC[0-9][0-9][0-9]","project_rec_number":"","project_rec_start_date":"","project_rec_end_date":"","project_end_type":"","project_token":"98F6A90C61CB397C49BE2F8F6FB2B222","project_id":"33","project_active":"1","project_annotation_type":"","project_pat_import_folder":"","project_import_rule_1_tag":"","project_import_rule_1_regexp":"","project_use_autoid":"","project_autoid_aetitle":"","project_tsd_group":"","project_tsd_id":"","project_tsd_user":"","project_tsd_folder_name":"","project_features___0":"0","project_features___1":"0","project_features___2":"0","project_features___3":"0","project_end_request":"","project_end_request_who":"","project_end_data":"","project_end_table":"","project_end_dictionary":"","project_end_data_forwarded":"","project_end_confirm":"","project_end_ok":"","project_end_delete_sign":"","projects_complete":"0","rewrite_ex_tag":"","rewrite_ex_reg":"","rewritepixelexclusions_complete":"","tech_sender_role":"","tech_role_other":"","tech_pass_policy":"","tech_pass_helpdesk":"","tech_transfer":"","tech_save_rest":"","tech_logging":"","tech_who_has_access":"","tech_deliver_results":"","tech_backup":"","tech_end_project":"","tech_secondary_use":"","tech_sub_contractors":"","tech_name":"","tech_date":"","tech_sign":"","technical_capabilities_questions_complete":"0"}]
```

another example (adding records, forms and events):

```
#!/bin/sh
DATA="token=921AD1F8B4ADA41EA7A05696888CC83D&content=record&action=export&format=json&type=flat&csvDelimiter=&records[0]=1462-0004&records[1]=1490-0004&fields[0]=leave_alone_notes&fields[1]=leave_alone_current_rek&fields[2]=leave_alone_mrn&fields[3]=leave_alone_do_not_touch_complete&forms[0]=leave_alone_do_not_touch&forms[1]=projects&forms[2]=rewritepixelexclusions&events[0]=event_1_arm_1&events[1]=baseline_arm_1&events[2]=m5y1_arm_1&rawOrLabel=raw&rawOrLabelHeaders=raw&exportCheckboxLabel=false&exportSurveyFields=false&exportDataAccessGroups=false&returnFormat=json"
CURL=`which curl`
$CURL -H "Content-Type: application/x-www-form-urlencoded" \
      -H "Accept: application/json" \
      -X POST \
      -d $DATA \
      https://fiona.ihelse.net:4444/api/
```

# Research information system

A web-based application that uses the above API to store data. The web-application allows users to login (authorization and authentication using OAuth2.0). Admin users can create (enable) new (normal) user accounts, create projects and assign users to projects given a role. All access to data in the backend should be logged (change, create, delete table and a separate view table).
The web application is implemented in PHP. The PHP layer handles page rendering, sessions, and the OAuth2/LDAP login flow; all data access to the backend goes exclusively through the API.

# User interface to administer the backend

The user interface should exclusively use the API to create projects, edit the setup of a project and to store values into fields for projects.

Provide an admin interface that allows authenticated and authorized users to create new projects, setup a project (add arms, events, and instruments). Allow the user to reorder the list of instruments. Make the field record of the first instrument automatically the record_id field for this project. Provide a "designer" web application that allows to select an instrument and to edit, add and change its fields and the order of fields. Add a page to edit the instrument by event mapping (table with checkboxes) for each arm. Assume there is only a single arm to start with.

# User interface to add data

Users login and see a list of projects they have access to.

User selects a project and the project screen shows a summary of the project (number of records, instruments, fields).

The project screen allows the user to "Setup" (permission "project_admin", add/remove arms, instruments, events, mappings between them), "Design" (create a new instrument, edit fields in an existing instrument), "Record status dashboard" (show table of records and instrument by events) and "Export" (export data for a project based on the users permissions for export).

Create a projects "record status dashboard" that lists all record_ids in a project with their instruments sorted by event and ordered based on the order of instruments in each arm. Each instrument should be rendered with a small graphic that indicates if any of the fields in that instrument have a value or if none of the fields have a value (gray circle).

# Projects

A project should only be visible to a user if they are in the administrator group, or if they are a member of the project. All members of a project that are not assigned to a project role should have full permissions. The interface should only present objects to sub-pages if the role/permission of the user allows them to use it.

# Export formats

Support two export formats. a) "Export as csv (raw)" and b) "Export as csv (labels)". For the raw format multiple-choice fields should export with their numeric values. For export type "labels" instead save the text of the field choice. 

To save a multi-valued field such as a checkbox type field (multiple answers) save one column for each choice. Use the fields name (lower-case with underscores and warning if over 26 characters) followed by two underscores and the numeric value. Here an example. A checkbox field called "demo_habits" with two choices coding 

```
1, smoking
2, drinking
```

would be written in the csv exported file as one column "demo_habits__1" and a second column "demo_habits__2" for the two choices (1/0 coding, export type "raw").

# User interface details

Use the latest bootstrap templates (v5.x). After logging in the main website should have functionality in a sidebar window. Selecting different functions (like setup) should load the corresponding page in the right hand panel.

The use flow should start with a) login, b) present a list of the projects the user has access to and c) opening a projects home page.
On the projects home page the user can select options to i) setup the project, ii) to design all instruments, iii) to create arms and events, to iv) assign instruments to arms and events and to v) administer users in the project (match roles to permissions) and to iv) export.
After finalizing the setup of the project the "Record Status Dashboard" should be used to a) list all participants and to b) create a new participant. Creating a new participant (enter record_id string) that participants overview page should be shown, listing all arms, instruments and events with a color-code (no data, some data, finished data entry). Such color codes (dropdown) should be assigned by the user at the end of each data collection instrument (not for surveys).
Selecting an instrument on the participant page should open the list of fields for that participant (existing values filled into all fields). Based on the users permission values can be changed in this view.

As arms will be used rarely all arm dependent section can be hidden using a tab-interface. The information of the first arm (arm_1) should be displayed by default - for example the instrument-event mapping as instruments as rows and events as columns (table entries as checkboxes). An instrument can be assigned to none, one or several events. Adding a new event (allow changing of event order) should allow the user to assign more instruments to the new event.


# Details

## Questions: 
"ASM-API-3 (normative) + the audit design both say UI data entry submits as content=record&action=import against the data API, 'initiated by the PHP layer with the user's project token.'
But the session is specified to store identity only — Authentication_Authorization_Design.md:120: 'project tokens live in user_projects.token, never in the session' — and no admin-API endpoint returns a member's project token (only the one-time add/rotation response does)."
Answer: Allow the admin-API endpoint to return a member's project token.

Support different time zones for the internal storage of dates and times. Support projects that collect data in different time zones using the browser timezone information.

Some "projects" table information is better stored in an instrument of a project "DataTransferProjects". Simplify the table projects and remove information such as the options (pathology, radiology, etc.), end_provision and event names. Keep information about the PI and the field for REK number. Keep also a field for the main supporting institution.

Authentication ("users" table) should also support a table-based authentication options. If first installed and not linked to either LDAP or oauth a table-based admin account should allow the user to setup the research electronic data capture systems authentication and authorization in the user interface.

To identify a user from oauth and LDAP use their institutional email address.

User accounts should have a limited time (days) they are valid. That time can be "0", which is indefinite.

User accounts that do not have a login in the last N days (180) should be "disabled". An admin user needs to "enable" them again before the user can gain access to the system again. Display such information for the admin user on the user overview screen (used to assign users to projects, etc.).

Add a by-user configurable two-factor authentication. It should cover a phone-based key option and an email option.

The generateNextRecordName is only for projects with the property "auto-generate-record-names". Such a project uses integers (start with 1) as record_ids. The second options for projects is to be "user-defined-record-names". Such names are strings like "<project acronym>_<numeric site code>_<numeric value with leading zeros>".

For authentication with LDAP use ext-ldap native php and for OAuth workflows use a library such as jumbojett/OpenID-Connect-PHP.

For project documentation purposes create a user view that shows the data dictionary of the project (list of instruments and their fields) as a table. Add a feature to export the data dictionary similar to the assets/Example_data_dictionary csv file.

Project name: CLARA - "Clinical Logbook for Automated Research Assistance", Related to a light-towers log-book

## Some more details

Create some administration instructions as human readable markdown files covering initial setup and testing.

## Upgradability and versioning

Upgrades: Support either a full installation or, a rolling versioned update installation. The update installation should adjust existing tables without loosing collected data (projects, instruments, fields, arms, roles, etc.). To update an installation from a compatible version to the current update installation a folder copied to the installation directory should be sufficient. Use the folders name (like clara_v1.0.0) to indicate the update installation version number. The user interface should allow admin users to start a versioned update installation process. If the process is successful (bump the version number in the database tables).

## Project modes

Every project is in exactly one of three modes: development, production, or analysis. Only an installation admin user (`is_admin`) can change the mode — a project's own project admin cannot (owner decision 2026-09-27). Staging is run by the project admin as before.

### Development
- Default mode for new projects.
- Setup and data entry work as usual; all functionality available.

### Production
- Setup changes are staged and activated as a group:
  - Start staging — begins a staging set (e.g., add or modify instruments). While staged, ongoing data collection continues to use the currently active instrument versions.
  - Commit staged changes — activates the whole staging set at once.
- Breaking-change warning: before committing, warn about changes that would make existing project data inconsistent:
  - No warning (non-breaking): adding a field, changing a field description, adding options to an existing dropdown.
  - Warning (breaking): any change that makes recorded data inaccessible, e.g., deleting a field.

### Analysis
Data entry is disabled; viewing and exporting remain available according to each user's permissions. Admin users can still interact with the project.

- Setup changes are allowed for admin users and apply immediately — no staging set. A change that classifies as breaking warns first and applies only on the admin's confirmation, using the production classification (owner decision 2026-09-27).

### Transitions

| From → To	| Who |	Prompt / effect |
|-------|--------|--------|
| development → production	| installation admin	| Ask whether previously stored data should be kept or deleted |
| production → development	| installation admin	| Keep all data |
| production ↔ analysis	| installation admin	|  Keep all data |
| analysis → development	| installation admin	| Keep all data — the way out of analysis without passing through production again (owner decision 2026-09-27) |

A project enters analysis only from production, where the data entry it disables has actually happened; development → analysis is not offered. No mode change of any kind is possible while a staging set is open — commit or discard it first (owner decision 2026-09-27).

## Field validation

Field validation should be extensible by adding additional validations (regular expressions with a given name) to the database. Use the existing email and MRN entries, add an international phone number validation type such as "+47 55566777" as well as a national phone number type "55566777".

## Rate limitter

Using the API from the web-application or from external scripts should be rate-limitted based on the incoming IP address. Allow up to 600 calls per minute from a single IP source. Check if our setup allows for unique IP addresses from incoming calls. Make rate-limitting threshold values customizable in the administration interface.
If an IP hits the currently active rate limit all requests from that IP should be ignored for (configurable) 10min. After this period requests from that IP should be handeled again (restart rate limit check).

## Authentication order

- before a user logs in, they should be able to select an authentication source by name (resolves internally to table-based, or an LDAP(s) source or an OAuth provider)
- a name presented to the user for an authentication source should be similar to "Hospital 1", "Hospital 2", etc.
- allow for more than one authentication source to have the same name
- allow for multiple names per authentication source
- for login try each authentication source with the same name in parallel - first "login ok" response wins

## Instrument level completion info

The 3-state record completion (binary today, REQ-API-074) is by instrument only (fix REQ-API-074). The user interface should allow a user to set the instrument completion code as a new (always displayed last in the instrument) field in the "record instrument" web view. If the instrument is marked as a survey this completion info is filled in automatically (complete, green).

## Record history by field

The "record instrument" web view shows all fields for an instrument with values by record, arm, repeating instrument. The web views shows after clicking an instrument button/icon in the "record status dashboard". In the record instrument web view next to each fields description (only editable in designer) and value (editable in record instrument) a small "history" button should allow the user to see a table with previous values (record-history with dates value was entered and the user account). See also `API_Endpoints_Design.md` §4.16.

## Arms, events and instruments

In all projects there should always be at least one arm ("arm_1"), event ("baseline") and instrument ("instrument"). Adding, deleting and re-ordering of arms, events, and instruments should be possible. Re-ordering should not change the arm, instrument, or events {id}. If the user deletes the last instrument, only the fields in that instrument should be deleted. The instrument should be renamed to "instrument". It should not be possible to delete the last remaining arm or event but a) the arm is renamed to "arm_1" and the event is renamed to "baseline" and b) the events offset days are reset to 0 +-0.

A new project should display only two fields in its first "instrument". The record_id field (always first field in first instrument) and the complete (instrument level) dropdown field, which is always the last field. Deleting the last remaining instrument should result in the same setup.

## Web interface generals

The assets/table_based_authentication... is an example for a partial admin screeen (user accounts) only. Other pages/partial screens/applications are expected to have their relevant code in their own js/all.js.

Later: Build a new AC.php for web/ fixing issues and extending it to support table-based, LDAP(s) and OAuth flows.

## Translations workflow

In the translation workflow the admin users request (export) from the admin web interface a table with user interface translations (csv, 1 column for english and 1 column for the target translation language). They upload a filled in table (csv) with all or some of the target language translations. The upload should trigger LANGUAGE_CACHE to invalidate. A user can select his standard language on first login (store as part of the user info) or change the standard language later on the users profile page (list users email as well).

## Technical debt

Keep a list of technical debt. Include things like node, nvm, composer, php and all imported external libraries. Include expected range of version numbers (if known) the solution is likely to work with (like would php7 still work or are we using features that only exist in php8?). The overview should be sufficient to evaluate the technical debt of Clara separated into development environment (building Clara), api/ and web/. Add this list to the existing documentation (maybe in docs/index.md?).


## Later: alternative admin WebLLM interface

For admin users add a webllm based interface that can access the api (read only mode, or read and write) through an mcp server. The webllm application should preload a model for tool use and stream responses to the user (see for example https://github.com/OpenLinkSoftware/WebLLM-Tools-Sample). Use cases are:
- request information about projects
- statistics about stored data in a project (marginal statistics, t-tests and linear mixed effects models)
- answer questions about data stored in different projects - like distribution of "sex at birth" variables by project

## Better documentation

For Requirements/, Plan/, and Design/ documents provide a classification of relevance for all codes into: "security", "performance", "workflow", "scalability", "resilience". Keep that information as a grouped summary table (Requirements/*.md, relevance, code list) as part of docs/index.md.

## Performance metrics as part of api

The api should measure performance metrics like response times and memory amounts for database operations and api internal data processing. The information should be sufficient to later evaluate extensions to the infrastructure that runs the api and to the database system that the api talks to.

## Add an is_admin "system administrator" flag to user

The bootstrap user with "is_admin" should be used during setup of the system only. The bootstrap user should at any point be able to make another user account "is_admin" with all system permissions ("system administrator"). An api endpoint should allow is_admin users to assign "is_admin" to other existing users (is admin group). The administration user interface should display current system administrators add/remove this permission. At least one is_admin user should always exist (including the bootstrapped account).
Prevent all is_admin users from loosing access. 


## Error messages by api

If the API cannot fulfil the request of the user to import data it should generate an error message like the following:

```
{"error":"The following values of redcap_event_name are invalid: v1_arm_1_arm_1"}
```

Data producing the error: 

```
[
    {"record_id": "TNT-RECORD-17-025", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-025", "last_name": "TNT-RECORD-17-025"}, 
    {"record_id": "TNT-RECORD-17-019", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-019", "last_name": "TNT-RECORD-17-019"}, 
    {"record_id": "TNT-RECORD-17-020", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-020", "last_name": "TNT-RECORD-17-020"}, 
    {"record_id": "TNT-RECORD-17-021", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-021", "last_name": "TNT-RECORD-17-021"}, 
    {"record_id": "TNT-RECORD-17-022", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-022", "last_name": "TNT-RECORD-17-022"}, 
    {"record_id": "TNT-RECORD-17-023", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-023", "last_name": "TNT-RECORD-17-023"}, 
    {"record_id": "TNT-RECORD-17-024", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-024", "last_name": "TNT-RECORD-17-024"}, 
    {"record_id": "TNT-RECORD-17-026", "redcap_event_name": "v1_arm_1_arm_1", "first_name": "TNT-RECORD-17-026", "last_name": "TNT-RECORD-17-026"}
]
```


# Security relevant findings

Fixed findings carry a **Status** line with the date and how the fix landed; the original finding text is kept unchanged as the record of what was wrong.

### F1 — Local-password brute force bypasses lockout and audit (High)

- **Where:** `web/app/Auth.php:95`, `api/internal/admin/auth.go:196`.
- **What:** The web login calls `POST /api/v1/auth/verify-password` first and calls `login` only on success. `verifyPassword` neither checks `h.lockouts.locked` nor records a failure, and writes no audit entry. Its comment says the brute-force check runs in PHP, but PHP has none. `tests/php/router_test.php:241` codifies the behaviour.
- **Impact:** Unlimited online guessing of any local account, including the bootstrap admin. No `login_failure` audit trail. The IP rate limiter is off by default (`rate_limit_enabled` opt-in).
- **Fix:** In `verifyPassword`, reject when the address is locked and call `h.lockouts.failure` on a bad password. Alternatively, have PHP finalize every failed race with a `login` call carrying `attempts`, as the design describes. Turn the IP rate limiter on by default for `/api/v1/auth/*`. Add a regression test.
- **Status:** Fixed 2026-10-02 (server-side option). `verifyPassword` rejects a locked address with `429 rate_limited`, counts every bad password toward the Sequence E window, and writes a `login_failure` audit entry; the IP rate limiter stays on for `/api/v1/auth/*` even when `rate_limit_enabled` is off. Regression tests `TestVerifyPasswordLockoutAndAudit` and `TestRateLimitAlwaysOnForAuthPaths`; `Design/Authentication_Authorization_Design.md` §2.3/§2.6/§2.9 updated to match.

### F2 — Audit endpoint leaks live tokens and PHI to any member (High, Req-change)

- **Where:** `api/internal/admin/queries.go:271` (`listAudit`), `auditViewEntry.Token`; REQ-API-078, REQ-AUD-018.
- **What:** A non-admin only needs membership of the project. A member with `no_access` can call `GET /api/v1/audit?type=views&project=N` and receive the plaintext data-API token of every member who exported records. `type=events` returns `details` with old/new values of every changed record, ignoring the caller's arm levels and DAG.
- **Impact:** Privilege escalation, because a stolen token carries its owner's export and delete rights. It also discloses PHI the caller is not allowed to see.
- **Fix:** Restrict audit reads to `is_admin` or `project_admin`. Store a token fingerprint (for example the first 8 hex digits of SHA-256) instead of the token. Filter or redact `details` by the caller's arm and DAG visibility.
- **Status:** Fixed 2026-10-02 via the access-restriction option, tightened to `is_admin` only (`project_admin` also rejected). `listAudit` now runs through `requireAdmin`; REQ-API-078, ASM-API-2, REQ-AUD-019 and the Design documents (API_Endpoints §4.15, Audit_Logging §7, User_Interface route table) updated to match; regression in `TestListAudit` (member and outsider both get 403). The token-fingerprint and `details`-redaction sub-fixes were not implemented — with reads reserved to `is_admin`, they remain defense-in-depth against an administrator's own view only.

### F3 — Survey link writes any field of its record (High)

- **Where:** `api/internal/dataapi/record_import.go:205`.
- **What:** The link check pins `record_id` and `form_name`. However, the field loop accepts every dictionary field present in the row, not only fields of `form_name`.
- **Impact:** An anonymous respondent can overwrite clinician-entered data, identifiers or other instruments of their record. The overwrite is audited but not prevented.
- **Fix:** Reject or ignore fields whose `Instrument != formName`, except the record identifier. Apply the same rule to project tokens, so `form_name` means what it says.
- **Status:** Fixed 2026-10-02. The import field loop rejects any field whose instrument is not the request's `form_name` (the record identifier stays exempt), for survey links and project tokens alike — a row can now only write its own instrument. Regression tests `TestRecordImportRejectsForeignInstrumentFields` and `TestSurveyLinkCannotWriteForeignInstrumentFields`.

### F4 — DAG bypass on import (High)

- **Where:** `api/internal/dataapi/record_import.go:260`.
- **What:** Export and delete restrict to the caller's active group. Import loads the existing record and writes to it without comparing `entity.DagGroupID` to the caller's group.
- **Impact:** A site-A user can overwrite site-B records. The `added`/`updated` result also confirms that a record ID exists in another group.
- **Fix:** When the caller has an active group and the entity exists with a different group, return the same row-level failure as a validation error. Extend `TestRecordDAGScope` to cover import.
- **Status:** Fixed 2026-10-02. Import compares the caller's active DAG to `entity.DagGroupID`; an existing record in another group (or in no group) fails the row with `PERMISSION_DENIED`, matching export and delete visibility, while new records still join the caller's group (REQ-API-093). `TestRecordDAGScope` now covers import.

### F5 — Unbounded recursion in the expression parser (High)

- **Where:** `api/internal/validate/expr.go:532` and `:893` (recursive descent), called from `record_export.go:219`.
- **What:** Each `(` recurses through four functions with no depth limit. The data-API body may be 32 MiB. A Go stack overflow is a fatal runtime error that `recover` cannot catch, so the whole process exits.
- **Impact:** Any token holder with an export level can repeatedly crash the API for all users. Admin-entered branching and calculation expressions share the parser.
- **Fix:** Cap expression length (for example 4 KiB) and nesting depth (for example 64) in `tokenize` and the parsers. Parse `filterLogic` once per request instead of once per record.
- **Status:** Fixed 2026-10-02. `tokenize` rejects expressions longer than 4 KiB; a shared nesting guard caps parentheses, groups and unary operators at 64 levels across all three parsers (calculation, branching, logic). Export compiles `filterLogic` once per request via the new `validate.CompileLogic`. Regression tests `TestExpressionDepthLimit`, `TestExpressionLengthLimit`, `TestCompileLogic`.

### F6 — `filterLogic` re-identification in de-identified exports (Medium, Req-change)

- **Where:** `api/internal/dataapi/record_export.go:219`.
- **What:** Identifier and free-text columns are dropped below `export_full`. The filter, however, evaluates against all stored values.
- **Impact:** A `export_de_identified` user can probe with `[name] = 'Jane Doe'` or `[dob] = '1980-01-01'` and learn which record belongs to whom. This defeats GD-2's de-identification promise.
- **Fix:** Reject filters that reference fields outside the column set permitted at the applied export level.

### F7 — Plaintext data-API tokens at rest (Medium, Req-change)

- **Where:** `user_projects.token` (`api/internal/db/repo_identity.go:353`), `audit_events.token`, `audit_record_views.token`.
- **What:** Tokens are random (122 bits) but stored and logged verbatim. REQ-API-102 requires re-fetching the token, which forces plaintext storage.
- **Impact:** Read access to the database, a backup, or the audit tables yields working API credentials for every member.
- **Fix:** Store `SHA-256(token)` and look it up by hash. Show the token once at issue or rotation, and drop the re-fetch endpoint. For PHP's own UI data entry, use the admin API with `X-Internal-User-Id` rather than a member token. Audit a fingerprint only.

### F8 — Service token as master key; shared listener (Medium)

- **Where:** `api/internal/httpapi/httpapi.go:34-37`, `admin/auth.go:97-117`, `config/config.go:235`.
- **What:**
  - Whoever holds `INTERNAL_SERVICE_TOKEN` can act as any user through `X-Internal-User-Id`.
  - With the token, `source: oauth2` logs in as any existing user with no credential and no 2FA.
  - `/api/v1/` is served on the same listener as the public `/api/`. Isolation depends entirely on an nginx config that is not in the repo.
  - There is no minimum token length.
  - `APP_ENV` defaults to `development`, where `dev-internal-token` is accepted.
- **Impact:** One proxy misconfiguration, or a forgotten `APP_ENV=production`, exposes the full admin API to the internet with a publicly known token.
- **Fix:**
  - Serve the admin API on a separate listener (a Unix socket or a loopback-only port).
  - Require at least 32 random bytes for the token, and default `APP_ENV` to production.
  - Ship and test a reference nginx config that strips the `X-Internal-*` headers.
  - Consider a short-lived signed assertion from PHP (HMAC over user ID, timestamp and method/path) instead of a static bearer.

### F9 — Spoofable client IP (Medium)

- **Where:** `web/app/Request.php:139`.
- **What:** `clientIp()` returns the browser's `X-Real-IP` header when present, and forwards it to the API. The API trusts it because PHP is a trusted proxy. Safety depends on nginx overwriting `HTTP_X_REAL_IP` with a `fastcgi_param`.
- **Impact:** Attackers rotate fake IPs per request to defeat the rate limiter, and forge the IP in `password_reset_requested` audit entries.
- **Fix:** In PHP, use `REMOTE_ADDR` unless it is inside a configured trusted-proxy list, mirroring `SourceIP` in Go.

### F10 — Bootstrap admin auto-promotion via external IdPs (Medium, Req-change)

- **Where:** `api/internal/admin/auth.go:106`; REQ-AUTH-007, GD-4.
- **What:** Every OAuth2 or LDAP login whose email equals `ADMIN_BOOTSTRAP_EMAIL` creates or re-enables the account and sets `is_admin = 1`. That includes an account another admin deliberately disabled.
- **Impact:** Whoever controls that address at any configured IdP becomes an admin. Examples are a Google provider that accepts unverified or personal addresses, or an LDAP `mail` attribute a user can edit. Disabling the bootstrap admin also cannot stick.
- **Fix:**
  - Promote only on the local source, or only while no other admin exists.
  - Never re-enable a disabled account.
  - Require `email_verified = true` from OIDC providers.
- **Status:** Fixed 2026-10-02 for the first two bullets. Login-time promotion now creates the row only when absent, never re-enables a disabled account, and promotes an existing enabled row only on a `local` login or while no other enabled administrator exists (`Store.EnsureBootstrapForLogin`; REQ-AUTH-007 revised, DEV-AUTH-15). Administration after setup is assigned through `PUT /api/v1/users/{id}` with `is_admin` (REQ-API-136), and at least one enabled administrator always exists (REQ-AUTH-068). Regression tests `TestAuthBootstrapDisabledStaysDisabled`, `TestAuthBootstrapNoRePromotion`, `TestUpdateUserAdminFlag`, `TestLastAdminRevocationRace`. The third bullet (`email_verified = true` from OIDC providers) is not implemented — OIDC assertions are handled in the PHP layer, where it remains open work.

### F11 — Survey links do not expire (Medium, Req-change)

- **Where:** `api/internal/dataapi/auth.go:234`.
- **What:** Only `revoked` is checked. A link stays valid for re-submission indefinitely.
- **Impact:** A forwarded or leaked link lets anyone rewrite a completed response at any time.
- **Fix:** Add `expires_at` and an optional lock once the instrument is marked complete.

### F12 — No password policy (Medium, Req-change)

- **Where:** `api/internal/admin/passwords.go:106`, `changeMyPassword`, `bootstrapAdmin`.
- **What:** Any non-empty password is accepted. bcrypt rejects passwords over 72 bytes, which surfaces as a 500.
- **Fix:** Enforce a minimum length of 12 and a maximum of 72 bytes with a clear 400. Optionally check a local breached-password list. Apply the same rule to `ADMIN_BOOTSTRAP_PASSWORD` at startup.
- **Status:** Fixed 2026-10-02 (breached-password list not implemented). Minimum 12 characters / maximum 72 bytes with a clear 400, enforced on setup completion (after token validation), self-service change (after the current-password check), user create/update, and `ADMIN_BOOTSTRAP_PASSWORD` at startup — no policy-free path remains. Regression tests `TestPasswordPolicyOnSetupCompletion`, `TestPasswordPolicyOnSelfServiceChange`, `TestValidatePasswordPolicy`.

### F13 — Account enumeration (Low)

`admin/auth.go:88` answers `account_not_found` for an unknown email and `bad_password` for a wrong password. It also skips bcrypt for unknown users, so response timing differs. Password-reset requests send mail synchronously only for real accounts. **Fix:** Return one code. Run a dummy bcrypt comparison for unknown users. Send reset mail asynchronously.

### F14 — Open redirect (Low)

`LoginController.php:90` accepts `next=/\evil.example`, which browsers normalize to `//evil.example`. **Fix:** Reject backslashes and control characters. Better, require `parse_url` to yield only a path.

### F15 — Unbounded limiter maps and targeted lockout (Low)

The `authLockout`, `addressLimiter` and `sendLimiter` maps (`lockout.go:35`, `passwords.go`, `tfa.go`) are never swept. Random email addresses grow memory without bound. Anyone can also lock out a known user, including the admin, for 15 minutes at a time. **Fix:** Sweep these maps as `RateLimiter` already does. Key the lockout on (email, IP) as well as email, or add a CAPTCHA-free backoff.

### F16 — Missing server timeouts (Low)

`cmd/server/main.go:86` sets only `ReadHeaderTimeout`. Slow request bodies, up to the 32 MiB cap, can hold connections open indefinitely. **Fix:** Set `ReadTimeout`, `WriteTimeout` (generous for exports), `IdleTimeout` and `MaxHeaderBytes`. **Status:** Fixed 2026-10-02 — `ReadTimeout` 5 min (a full import on a slow uplink), `WriteTimeout` 10 min (large exports), `IdleTimeout` 2 min, `MaxHeaderBytes` 1 MiB.

### F17 — Session lifecycle (Low)

PHP sessions have an absolute lifetime only, with no idle timeout. A password change or reset does not end other sessions (DEV-AUTH-13). `SESSION_COOKIE_SECURE` defaults to `0`, and `SESSION_DIR` points at `/tmp`. **Fix:** Add an idle timeout of about 30 minutes. Store a per-user session epoch that the API bumps on password change. Refuse non-secure cookies when `APP_ENV=production`. Use a private 0700 session directory.

### F18 — 2FA race conditions (Low)

Recovery-code use (`tfa.go:388`) and the TOTP last-step update are read-modify-write without a conditional `UPDATE`. Two parallel requests can redeem the same code. TOTP secrets are stored unencrypted. **Fix:** Use `UPDATE … WHERE last_step < ?` and compare-and-swap on the recovery JSON. Optionally encrypt secrets with a key from the environment.

### F19 — Token in the query string (Low, Req-change)

`params.go:91` accepts `token` from the URL (REDCap compatibility), so tokens end up in proxy and access logs. **Fix:** Accept the token from the POST body only. If GET compatibility is required, scrub `token=` from the nginx log format.

### F20 — Fail-open role default (Info, Req-change)

A member with no role holds `edit_survey_responses`, `export_full` and `project_admin` (REQ-AUTH-022, `dataapi/auth.go:198`). Forgetting to assign a role therefore grants everything. **Fix:** Consider requiring an explicit role, or defaulting to no access, and show a warning in the UI for role-less members.


## User page flow

The screen "project overview" page, after logging in should only show a single panel (plus header and footer). If an is_admin user is logged in the header should have a button "Control Panel" (all admin related setting are on a separate page "Control Panel", like setting up permissions and roles for users). In the project overview middle panel is a table with projects the current user has access to, as rows including the stats for each projects in columns. Selecting one project (click on project name) should open that projects "project page" with a left side panel with options "Setup", "Record Status Dashboard", and "Export". On the right-hand panel show the corresponding page (Setup, Record Status Dashboard, Export). Options on the left side panel should only appear if the user has permissions.

## Role adjustment

A projects role has permission groups for "data access" and "export". In an arm permissions are specific to each individual instrument and event. The user interface displays a "Define/Edit a roles" as a table (per arm). Each row of the table are the permissions for an instrument (event). Columns display radio buttons for data access and export. Additional permission are coded as checkboxes. a) delete a records instrument (delete the instruments field values), b) edit already collected surveys.

┌────────────────────┬───────────────────┐
│ Permissions arm_1  │ Permissions arm_2 │   
├──────────────┬─────┴───────────────────┴───────────────────────┬────────────────────────────────────────┐
│              │ data access                                     │ export                                 │
│  instrument  ├────────┬──────┬──────┬────────────┬─────────────┼──────┬────────────┬─────────────┬──────┤
│  (event)     │ no     │ read │ view │ delete     │ edit survey │ none │ de-        │ no-         │ full │
│              │ access │ only │ edit │ [checkbox] │ [checkbox]  │      │ identified │ identifiers │      │
├──────────────┼────────┼──────┼──────┼────────────┼─────────────┼──────┼────────────┼─────────────┼──────┤
│ instrument 1 │ (O)    │ ( )  │ ( )  │    [ ]     │     [ ]     │ (0)  │ ( )        │ ( )         │ ( )  │
│ (baseline)   │        │      │      │            │             │      │            │             │      │
├──────────────┼────────┼──────┼──────┼────────────┼─────────────┼──────┼────────────┼─────────────┼──────┤
│ instrument 1 │ ( )    │ (O)  │ ( )  │    [ ]     │     [ ]     │ ( )  │ ( )        │ (O)         │ ( )  │
│ (followup)   │        │      │      │            │             │      │            │             │      │
├──────────────┼────────┼──────┼──────┼────────────┼─────────────┼──────┼────────────┼─────────────┼──────┤
│ instrument 2 │ ( )    │ ( )  │ (O)  │    [X]     │     [X]     │ ( )  │ ( )        │ ( )         │ (O)  │
│ (baseline)   │        │      │      │            │             │      │            │             │      │
├──────────────┼────────┼──────┼──────┼────────────┼─────────────┼──────┼────────────┼─────────────┼──────┤
│ instrument 2 │ ( )    │ ( )  │ (O)  │    [X]     │     [X]     │ ( )  │ ( )        │ ( )         │ (O)  │
│ (followup)   │        │      │      │            │             │      │            │             │      │
└──────────────┴────────┴──────┴──────┴────────────┴─────────────┴──────┴────────────┴─────────────┴──────┘

# Data model for clinical study management system

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

Questions: 
"ASM-API-3 (normative) + the audit design both say UI data entry submits as content=record&action=import against the data API, 'initiated by the PHP layer with the user's project token.'
But the session is specified to store identity only — Authentication_Authorization_Design.md:120: 'project tokens live in user_projects.token, never in the session' — and no admin-API endpoint returns a member's project token (only the one-time add/rotation response does)."
Answer: Allow the admin-API endpoint to return a member's project token.

Support different time zones for the internal storage of dates and times. Support projects that collect data in different time zones using the browser timezone information.

Some "projects" table information is better stored in an instrument of a project "DataTransferProjects". Simplify the table projects and remove information such as the options (pathology, radiology, etc.), end_provision and event names. Keep information about the PI and the field for REK number. Keep also a field for the main supporting institution.

Authentication ("users" table) should also support a table-based authentication options. If first installed and not linked to either LDAP or oauth a table-based admin account should allow the user to setup the research electronic data capture systems authentication and authorization in the user interface.

To identify a user from oauth and LDAP use their institutional email address.

User accounts should have a limited time (days) they are valid. That time can be "0" which is indefinite.

User accounts that do not have a login in the last N days (180) should be "disabled". An admin user needs to "enable" them again before the user can gain access to the system again. Display such information for the admin user on the user overview screen (used to assign users to projects, etc.).

The generateNextRecordName is only for projects with the property "auto-generate-record-names". Such a project uses integers (start with 1) as record_ids. The second options for projects is to be "user-defined-record-names". Such names are strings like "<project acronym>_<numeric site code>_<numeric value with leading zeros>".

For authentication with LDAP use ext-ldap native php and for OAuth workflows use a library such as jumbojett/OpenID-Connect-PHP.

For project documentation purposes create a user view that shows the data dictionary of the project (list of instruments and their fields) as a table. Add a feature to export the data dictionary similar to the assets/Example_data_dictionary csv file.

Bootstrap: For all tables use condensed tables (table-sm class).

Use responsible tables and adjust to smaller screens like tablets and phones.

Project name: CLARA - "Clinical Logbook for Automated Research Assistance", Related to a light-towers log-book

The assets/table_based_authentication_plus_user_management/ folder contains a historic FIONA user management application (table-based authentication). "AC.php" is the corresponding authentication control script that all FIONA pages are using to establish a session. **If not against otherwise specified requirements** plan the development to utilize the example layout and style of interfacing php with the web-application - pull data using json from the backend, populate rendering targets on the client.

## Some more details

Create some administration instructions as human readable markdown files covering initial setup and testing. 

Javascript libraries like bootstrap and fonts, css should be downloaded from an CDN once and placed into local directories (where accessible). The final application pages are expected to work without access to internet so loading from local copies is the best solution.

For the web interface a nice font seems to be font Geist (https://fontsource.org/fonts/geist/use).

Do not use web-pack or similar technology that requires a build step for the website frontend.

For performant table rendering on the website use this javascript library: https://unpkg.com/tabulator-tables.

## Upgradability and versioning

Upgrades: Support either a full installation or, a rolling versioned update installation. The update installation should adjust existing tables without loosing collected data (projects, instruments, fields, arms, roles, etc.). To update an installation from a compatible version to the current update installation a folder copied to the installation directory should be sufficient. Use the folders name (like clara_v1.0.0) to indicate the update installation version number. The user interface should allow admin users to start a versioned update installation process. If the process is successful (bump the version number in the database tables).

## Project modes

Projects should be in one of three modes - development, production, analysis. Admin users for a project should be able to set this project mode. Projects start in "development" mode with setup and data entry as usual. All functionality should work as expected. A project in development mode can be moved to "production" mode (by project admin user, ask if previously stored data should be deleted or kept). In production mode changes to the setup are staged together (start staging) before they become activated together (commit staged changes). The user can for example create addititional instruments or change the existing instruments (after start staging). During this phase all ongoing data collections are still using the currently active versions of all instruments. At any point in production the user can decide to "commit" the staged changes. A warning should inform the user which of the changes will make the database for the project inconsistent. For example, adding a new field to an instrument or changing its description should not be considered a breaking change. Adding new options to existing dropdown menues are the same (no warning). Warn the user if data is no longer accessible (deleting a field). A project in production mode should also be able to moved back to development. In mode "analysis" no more data entry should be possible but all users still have access and can export - based on their permissions.

## Add missing AGENTS.md

To synchronize development across several LLMs add AGENTS.md files into folders that benefit from it. Outline the rules for LLMs based on the existing project structure with Requirements, Plan, and Design documentation.

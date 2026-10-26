# Create data collection forms

Data is collected in instruments (forms) at events (visits). Set up the events on **Setup**; the instruments and their fields are made on **Design**. You need the project administrator right.

1. Open the project and choose **Setup**. Under **Events**, type the event label and, if the visit has one, its timepoint in days, then choose **Add event**.
2. Choose **Design** in the left panel. Below the instrument list, type a name for the new instrument and choose **Add instrument**.
3. Back on **Setup**, in **Instruments × events**, tick the events at which each instrument is filled out and choose **Apply**. An instrument without a ticked event collects no data.
4. Open an instrument on **Design**. The first field of the first instrument is the record identifier (for example `record_id`).
5. Choose **Add field**. Give the field a name (lower case, digits and _), a label and a type. For radio buttons, dropdowns and checkboxes enter one code and one label per choice. Validation, **Required**, branching logic and the other settings are optional. Save the field.
6. The field list shows the instrument's fields in form order. Use the arrows to reorder, **Edit** to change, **Move** to move a field to another instrument, and **Remove** to delete it.

## Tip

Instruments can also be imported from REDCap instrument ZIPs or data dictionary CSV files on the **Design** page.

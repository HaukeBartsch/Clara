# Export data

Download the project's data as CSV or JSON, the project as a CDISC ODM XML file, or its data dictionary as a REDCap instrument ZIP. What the data file contains follows your export permissions.

1. Open the project and choose **Export** in the left panel.
2. Choose the format, whether choices are written as codes or labels, the column headers and, for CSV, the delimiter and decimal separator. **Applied sensitivity** shows the level your file is exported at. Choose **Download data**.
3. **Export project as CDISC ODM** writes the project into one XML file for another study system to read. Choose the **ODM version**, and under **Contents** either **Project and data** or **Project only** — the structure without any record values. Only administrators see this block.
4. **Download data dictionary** gives the project's design without any record values. The **Design** page of another project can import it.

## Good to know

- Every download is recorded in the audit log with the level that was applied.
- Turn on **Mark the file as UTF-8 for Excel** when the file is opened in Excel directly, so æ, ø and å display correctly.

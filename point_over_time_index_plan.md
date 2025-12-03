

We already have all the primitives to layer multi‑study + “view over time” on top. we mainly need:

- one new indexing API (multi‑study)
- one new “resolve point across studies” API
- a small tweak to your existing DICOMweb `/studies` search
- some light UI wiring in your main Next.js app + OHIF

anchor this to your existing handlers.

---

## 1. Backend: support selecting & indexing multiple studies

You already have:

- `GET /api/imaging/studies` → `ListImagingStudiesHandler` (returns all `ImagingStudy` docs for the user)
- DICOM access via `h.Dicom.StudyMetadataJSON` etc.

### 1.1. Add an index table / collection

You don’t yet store any per‑slice geometry. Add a new data model for the longitudinal index.

Conceptually:

```go
type IndexedSlice struct {
    // logical ownership
    PatientID          string  `firestore:"patient_id" json:"patient_id"`
    StudyID            string  `firestore:"study_id" json:"study_id"`                 // your ImagingStudy.StudyID
    StudyInstanceUID   string  `firestore:"study_instance_uid" json:"study_instance_uid"`
    SeriesInstanceUID  string  `firestore:"series_instance_uid" json:"series_instance_uid"`
    SOPInstanceUID     string  `firestore:"sop_instance_uid" json:"sop_instance_uid"`
    InstanceNumber     int     `firestore:"instance_number" json:"instance_number"`

    // geometry (ImagePositionPatient, ImageOrientationPatient, PixelSpacing)
    IPPX, IPPY, IPPZ   float64 `firestore:"ipp_x" json:"ipp_x"`
    RowDirX, RowDirY, RowDirZ float64 `firestore:"row_dir_x" json:"row_dir_x"`
    ColDirX, ColDirY, ColDirZ float64 `firestore:"col_dir_x" json:"col_dir_x"`
    RowSpacing, ColSpacing    float64 `firestore:"row_spacing" json:"row_spacing"`

    // precomputed plane for fast lookup: normal·x = d
    NormalX, NormalY, NormalZ float64 `firestore:"normal_x" json:"normal_x"`
    PlaneD                    float64 `firestore:"plane_d" json:"plane_d"`

    StudyDate       string    `firestore:"study_date" json:"study_date"`
    AcquisitionTime string    `firestore:"acquisition_time" json:"acquisition_time"`
    CreatedAt       time.Time `firestore:"created_at" json:"created_at"`
}
```
Stored in a new collection like `imaging_slice_index` keyed by `(study_id, series_uid, sop_uid, instance_number)`.

Even though Firestore isn’t perfect for numeric nearest‑neighbor, per‑patient + per‑study counts are small enough that filtering + in‑memory math is fine for v1.

### 1.2. New “index these studies” endpoint

You suggested **keeping this in your main web app**, not OHIF. That’s the right call: indexing is system‑level, not viewer‑specific.

Add something like:

- `POST /api/imaging/longitudinal/index`

Handler sketch:

- Request body from Next.js (using your existing `ImagingStudy.StudyID`):

```json
  {
    "studyIds": ["STUDY-P6527WJ5", "STUDY-ABCD1234"]
  }
```
- In handler:

    1. Resolve user via `GetUserIDFromRequest`.
    2. For each `studyId`:
        - Fetch `ImagingStudy` with `h.DB.GetImagingStudy`.
        - Check `study.UserID == userID`.
        - Enqueue or directly start a background job to index that study.

Index job per study:

- Uses `study.StudyInstanceUID` and your DICOM client:

    - `h.Dicom.StudyMetadataJSON(ctx, study.StudyInstanceUID)`

- Parses DICOM JSON datasets (same style as `handleDicomWebListInstances` and `handleDicomWebSeriesMetadata` already do).
- For each instance:

    - Extract:
        - `0020,0032` ImagePositionPatient
        - `0020,0037` ImageOrientationPatient
        - `0028,0030` PixelSpacing
        - `0008,0018` SOPInstanceUID
        - `0020,0013` InstanceNumber
    - Compute:
        - row/col direction vectors
        - plane normal and `plane_d`
    - Save an `IndexedSlice` document.

You’ll also want an index/status collection:

```go
type LongitudinalIndexStatus struct {
    StudyID          string    `firestore:"study_id"`
    UserID           string    `firestore:"user_id"`
    Status           string    `firestore:"status"` // "not_indexed" | "indexing" | "indexed" | "error"
    LastError        string    `firestore:"last_error"`
    UpdatedAt        time.Time `firestore:"updated_at"`
}
```
And a lightweight:

- `GET /api/imaging/longitudinal/index-status?studyIds=...`

This lets your Next.js app show per‑study “Indexed/Indexing/Not indexed” badges.

---

## 2. Backend: point‑resolve API for multiple studies

This is what OHIF will hit after a click.

Add:

- `POST /api/imaging/longitudinal/resolve-point`

Request body (from viewer/OHIF):

```json
{
  "patientId": "user-123",
  "frameOfReferenceUid": "1.2.3.4.5",
  "x": 12.3,
  "y": -45.6,
  "z": 78.9,
  "studyIds": ["STUDY-P6527WJ5", "STUDY-ABCD1234"]
}
```
Handler flow:

1. Validate user with `GetUserIDFromRequest`.
2. Optionally cross‑check that `patientId` == userID (or derive from token and drop it from payload).
3. Query `imaging_slice_index` for:

    - `user_id == userID`
    - `study_id IN studyIds`
    - `frame_of_reference_uid == frameOfReferenceUid`  
      (you’ll need to store FoR UID in `IndexedSlice` too)

4. In Go, for those slices:

    - Compute distance from point `(x,y,z)` to slice plane using `normal` and `plane_d`.
    - For the best N per study, compute projected (row, col); drop out‑of‑bounds results.
    - Pick the single best slice per study.

5. Return:

```json
[
  {
    "studyId": "STUDY-P6527WJ5",
    "studyInstanceUid": "...",
    "seriesInstanceUid": "...",
    "sopInstanceUid": "...",
    "instanceNumber": 42,
    "row": 212,
    "col": 190,
    "studyDate": "2024-01-02",
    "acquisitionTime": "101530"
  },
  ...
]
```
OHIF then uses your existing DICOMweb endpoints (`/api/dicomweb/studies/{StudyInstanceUID}/series/.../instances/...`) to load those instances.

---

## 3. Backend: small tweak to DICOMweb `/studies` (optional but helpful)

Right now, `handleDicomWebSearchStudies` does:

- If `StudyInstanceUID` query param is empty → it returns `[]`.

For OHIF multi‑study, it’s nicer if:

- `GET /api/dicomweb/studies` (no StudyInstanceUID filter) returns **all visible studies for the logged‑in user**, using `ListImagingStudiesByUser`.

Minimal change:

- In `handleDicomWebSearchStudies`:

    - Instead of:

```go
    if studyUID == "" {
        w.Header().Set("Content-Type", "application/dicom+json")
        w.WriteHeader(http.StatusOK)
        w.Write([]byte("[]"))
        return
    }
```
- Do:

```go
    if studyUID == "" {
        studies, err := h.DB.ListImagingStudiesByUser(ctx, userID)
        // convert each ImagingStudy to a DICOM JSON study object (like you already do for the one-study case)
        // and return them as a [] of DICOM JSON datasets
    }
```
This lets OHIF show a study browser and eventually select multiple studies inside the viewer.  
It’s not strictly required if you manage study selection entirely in your Next.js shell, but it’s a clean win.

---

## 4. Frontend: Next.js main app changes (non‑OHIF)

On your main web app where you show `/imaging/studies`:

1. **Checkboxes & “Index selected” button**

    - Use `GET /api/imaging/studies` (already there) to render the table/list.
    - Add a checkbox per study row.
    - “Index selected” button:
        - Calls `POST /api/imaging/longitudinal/index` with `{ studyIds: [...] }`.
        - Shows a toast “Indexing started; this may take several minutes.”

2. **Status badges**

    - Call `GET /api/imaging/longitudinal/index-status?studyIds=...`.
    - Show per‑study:
        - grey: Not indexed
        - blue: Indexing…
        - green: Indexed
        - red: Error (optional: tooltip with `last_error`).

3. **Launching OHIF with multiple studies (for “over time” mode)**

    - When user clicks “View over time”:
        - Require that all selected studies are “Indexed”.
        - Open OHIF route with all `StudyInstanceUID`s, e.g.:

```text
       /ohif/viewer?StudyInstanceUID=UID1&StudyInstanceUID=UID2&StudyInstanceUID=UID3
```
     - Adjust to whatever query format your OHIF config expects. The backend already accepts any valid StudyInstanceUID set via DICOMweb.

---

## 5. Frontend: OHIF wiring changes

Inside OHIF (or your custom OHIF mode/extension):

1. **Multi‑study awareness**

    - Ensure your mode reads **all** `StudyInstanceUID` params and loads them as separate display sets or viewports.
    - OHIF already supports multi‑study; if it’s currently only using the first UID, update your mode’s `routes` / `studyInstanceUIDs` parsing.

2. **Hook to the resolve‑point endpoint**

    - In your extension/tool:

        - On click in a “reference” viewport:
            - Use Cornerstone/OHIF utilities to get:
                - The current image’s `FrameOfReferenceUID`
                - The clicked world coordinates `(x, y, z)` in patient space
            - Build the list of **selected studies**:
                - Either from OHIF’s active studies (those `StudyInstanceUID`s), or from the list you passed in via URL.

        - Call:

          `POST /api/imaging/longitudinal/resolve-point` with the shape above.

        - For each result:
            - Navigate the corresponding viewport (for that study) to `sopInstanceUid`/`instanceNumber`.
            - Place crosshairs at `(row, col)`.

That’s enough to get a working “view this point over time across N indexed studies” for same FrameOfReferenceUID.

---

## 6. Summary of concrete changes

Backend:

- New Firestore collection `imaging_slice_index` (or similar) + model for per‑slice geometry.
- New endpoints:
    - `POST /api/imaging/longitudinal/index` (index selected studies).
    - `GET /api/imaging/longitudinal/index-status` (per‑study status).
    - `POST /api/imaging/longitudinal/resolve-point` (point → best slice per study).
- Small but useful change:
    - `handleDicomWebSearchStudies`: when no `StudyInstanceUID` param, return all user studies instead of `[]`.

Frontend:

- Next.js:
    - Checkboxes on study list + “Index selected” button calling new index API.
    - Status badges from `index-status`.
    - “View over time” button that opens OHIF with multiple `StudyInstanceUID`s.

- OHIF:
    - Ensure multi‑StudyInstanceUID routing is wired.
    - Add a tool/extension that:
        - On click → computes (x,y,z) + FoR → calls `resolve-point` → drives other viewports.

If you want, next step I can propose specific Go handler signatures for the three new endpoints, or sketch how to convert your existing DICOM JSON parsing into `IndexedSlice` writes.

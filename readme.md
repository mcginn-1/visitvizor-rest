

#### CORS must be set for the GCP buckets
  
```vv-gcp-cors.json```
```json  

[
  {
    "origin": ["http://localhost:3000"],
    "method": ["GET", "PUT", "HEAD", "OPTIONS"],
    "responseHeader": ["Content-Type", "x-goog-meta-*"],
    "maxAgeSeconds": 3600
  }
]
```

##### Set the CORS
``` gsutil cors set vv-gcp-cors.json gs://vv-storage-vault ```

```gsutil cors get gs://vv-storage-vault ```



### Checking DICOM Store

Make sure vv-dicom shows up.

2.2 See the import operation status explicitly

If you log the operation name (op.Name), you can inspect it:
```bash
  gcloud healthcare operations describe OPERATION_NAME \
  --location=us-central1 \
  --dataset=vv-dataset-1
  ```


(or omit --dataset depending on gcloud version; you can also use the full resource name from the log).

This will show:

•  done: true/false
•  Any error details
•  Metadata about what it imported/rejected.

2.3 Query the DICOM store for studies (QIDO-RS via gcloud)

To verify that something is in the store:
```bash
gcloud healthcare dicom-stores dicom-web studies search \       #### DOESNT WORK
  --dataset=vv-dataset-1 \
  --dicom-store=vv-dicom \
  --location=us-central1
  ```




That hits GET /studies on the DICOMweb endpoint and should return a JSON array of studies. You can also filter by StudyInstanceUID or dates later.

If you want instances just for debugging:
```bash
gcloud healthcare dicom-stores dicom-web instances search \    ##### DOESNT WORK
  --dataset=vv-dataset-1 \
  --dicom-store=vv-dicom \
  --location=us-central1
  ```

This confirms whether your ingest actually wrote DICOM objects.

#### Cross-check with Firestore

The worker also scans GCS and writes imaging_studies docs. To sanity-check:

•  In the Firebase console / Firestore UI, look at the imaging_studies collection.
•  There should be docs with:
◦  user_id
◦  session_id = your session
◦  study_instance_uid matching what QIDO returns.

```bash
gcloud healthcare dicom-stores list \
  --dataset=vv-dataset-1 \
  --location=us-central1
```




1. Identify your Healthcare service agent

It’s a Google‑managed SA with this pattern:

•  service-<PROJECT_NUMBER>@gcp-sa-healthcare.iam.gserviceaccount.com

Get your project number:

```bash

gcloud config get-value project
gcloud projects describe $(gcloud config get-value project) --format='value(projectNumber)'
 ```

Suppose that prints:

•  PROJECT_ID=vv-1-a
•  PROJECT_NUMBER=123456789012

Then your Healthcare service agent is:

•  service-123456789012@gcp-sa-healthcare.iam.gserviceaccount.com



2. Grant it read access on the imaging bucket

You need at least storage.objects.list and storage.objects.get on vv-storage-vault.

Simplest: grant Storage Object Viewer role on that bucket to the Healthcare SA.


```bash
PROJECT_NUMBER=$(gcloud projects describe $(gcloud config get-value project) \
--format='value(projectNumber)')

HEALTHCARE_SA="service-${PROJECT_NUMBER}@gcp-sa-healthcare.iam.gserviceaccount.com"

gsutil iam ch "serviceAccount:${HEALTHCARE_SA}:roles/storage.objectViewer" gs://vv-storage-vault
```   


If you prefer gcloud instead of gsutil:
```bash
gcloud storage buckets add-iam-policy-binding gs://vv-storage-vault \
--member="serviceAccount:${HEALTHCARE_SA}" \
--role="roles/storage.objectViewer"
```



That grants the Healthcare service agent enough rights to:

•  storage.objects.list and storage.objects.get in vv-storage-vault, 
which is exactly what dicomStores.import needs for your gcs_prefix.

If you later want it to be able to write to a different bucket (e.g., export), 
you’d grant roles/storage.objectCreator there as well. For import only, storage.objectViewer is sufficient.


## Imaging Header Scan after DICOM ingest

### Scan each file to create a ```dicomInstanceInfo```

```go
	// At this point import succeeded; scan headers under the same prefix and
	// create one ImagingStudy per StudyInstanceUID.
	//     studyInstances = a map of [string /* always studyId */][]dicomInstanceInfo
	//
	//			type dicomInstanceInfo struct {
	//				StudyInstanceUID  string
	//				SeriesInstanceUID string
	//				SOPInstanceUID    string
	//				Modality          string
	//				StudyDate         string
	//				StudyDescription  string
	//			}
	//
	//
	studyInstances, err := h.collectDicomInstances(ctx, msg.GCSPrefix)
	if err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("collectDicomInstances: %v", err),
		})
		return err
	}
```
	
	
### Loop the ```dicomInstanceInfo```s and create an ImagingStudy
	
```go
    ////////////////////////////////////////////////////////////////////////
	//
	//     Takes a flat header scan result from all the files 
	//
	//       a map of [string /* always studyId */][]dicomInstanceInfo
	//
	//       and rolls up the data by StudyID, with multiple Series and Modalities
	//
	//        (This is the data that feeds our Image Viewer summaries for a single study)
	//               /imaging/studies or detail /imaging/studies/{studyID}
	//
	//       study := &ImagingStudy{
	//			StudyID:            studyID,
	//			UserID:             sess.UserID,
	//			SessionID:          sess.SessionID,
	//			StudyInstanceUID:   studyUID,
	//			SeriesInstanceUIDs: seriesUIDs,
	//			ModalitiesInStudy:  modalities,
	//			StudyDate:          studyDate,
	//			StudyDescription:   studyDescription,
	//			NumInstances:       len(instances),
	//			GCSPrefix:          gcsPrefix,
	//			DicomStorePath:     dicomStorePath,
	//			CreatedAt:          time.Now().UTC(),
	//		}
	//
	//
	if err := h.createImagingStudiesFromInstances(ctx, sess, msg.GCSPrefix, studyInstances); err != nil {
		_ = h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
			"status":        "error",
			"error_message": fmt.Sprintf("createImagingStudies: %v", err),
		})
		return err
	}

	// Success: mark session as ready.
	if err := h.DB.UpdateUploadSessionStatus(ctx, msg.SessionID, map[string]interface{}{
		"status": "ready",
	}); err != nil {
		return fmt.Errorf("UpdateUploadSessionStatus(ready): %w", err)
	}  
```


## Imaging Results 

Each ImagingStudy doc gives you:

```text
•  study_id – your key
•  study_instance_uid
•  series_instance_uids[]
•  dicom_store_path – e.g.  
projects/vv-1-a/locations/us-central1/datasets/vv-dataset-1/dicomStores/vv-dicom

Cloud Healthcare’s DICOMweb root for that store is:

  https://healthcare.googleapis.com/v1/{dicom_store_path}/dicomWeb
  ```




package main


import (
	"context"
	"flag"
	"fmt"
	"log"
	"visitvizor-rest/dicomweb"
	_ "visitvizor-rest/dicomweb"

	// same module, so no extra import path needed if this file lives
	// inside visitvizor-rest; it can just use NewDicomWebClient directly.
)

///////////////////////////////////////////////////////////////////
//
/*

 go run ./cmd/dicom_tool \
 -action=meta \
 -study=1.3.6.1.4.1.11129.5.5.111396399857604

 go run ./cmd/dicom_tool \
 -action=meta \
 -study=1.2.840.113619.2.428.3.319444734.713.1759316145.343

 go run ./cmd/dicom_tool \
 -action=delete \
 -study=1.2.840.113619.2.428.3.319444734.713.1759316145.343



*/

//
/*

  go run ./cmd/dicom_tool \
 -action=retrieve \
 -study=... \
 -out=study.multipart

*/

//
//
//

func main() {
	var (
		studyUID  = flag.String("study", "", "DICOM StudyInstanceUID")
		action    = flag.String("action", "meta", "action: meta|retrieve|delete")
		output    = flag.String("out", "study.multipart", "output file for retrieve")
		projectID = flag.String("project", "vv-1-a", "GCP project ID")
		location  = flag.String("location", "us-central1", "Healthcare location")
		datasetID = flag.String("dataset", "vv-dataset-1", "Healthcare dataset ID")
		storeID   = flag.String("store", "vv-dicom", "Healthcare DICOM store ID")
	)
	flag.Parse()

	if *studyUID == "" {
		log.Fatal("-study is required")
	}

	ctx := context.Background()
	client, err := dicomweb.NewClient(ctx, *projectID, *location, *datasetID, *storeID)
	if err != nil {
		log.Fatalf("NewClient: %v", err)
	}

	switch *action {
	case "retrieve":
		if err := client.RetrieveStudyToFile(ctx, *studyUID, *output); err != nil {
			log.Fatalf("RetrieveStudyToFile: %v", err)
		}
	case "delete":
		if err := client.DeleteStudy(ctx, *studyUID); err != nil {
			log.Fatalf("DeleteStudy: %v", err)
		}
	case "meta":
		b, err := client.StudyMetadataJSON(ctx, *studyUID)
		if err != nil {
			log.Fatalf("StudyMetadataJSON: %v", err)
		}
		fmt.Println(string(b))
	default:
		log.Fatalf("unknown -action %q (use meta|retrieve|delete)", *action)
	}
}

//func main() {
//	ctx := context.Background()
//
//	cfg := LoadConfig() // uses your existing env-driven config
//	client, err := NewDicomWebClient(ctx, cfg)
//	if err != nil {
//		log.Fatalf("NewDicomWebClient: %v", err)
//	}
//
//	studyUID := "1.3.6.1.4.1.11129.5.5.111396399857604"
//
//	// Example: retrieve study to file
//	if err := client.RetrieveStudyToFile(ctx, studyUID, "study.multipart"); err != nil {
//		log.Fatalf("RetrieveStudyToFile: %v", err)
//	}
//
//	// Example: print metadata JSON
//	meta, err := client.StudyMetadataJSON(ctx, studyUID)
//	if err != nil {
//		log.Fatalf("StudyMetadataJSON: %v", err)
//	}
//	fmt.Println(string(meta))
//
//	// Example: delete the study
//	// if err := client.DeleteStudy(ctx, studyUID); err != nil {
//	//     log.Fatalf("DeleteStudy: %v", err)
//	// }
//}

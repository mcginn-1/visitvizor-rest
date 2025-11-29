### DICOMweb in a nutshell


```text
DICOMweb is a family of RESTful HTTP APIs for working with medical images and related metadata. It standardizes how to:
•  Query what’s available (QIDO-RS)
•  Retrieve images and metadata (WADO-RS/WADO-URI)
•  Store new objects (STOW-RS)

Key services
1) QIDO-RS (Query based on ID for DICOM Objects)
   •  Purpose: Search for studies/series/instances by DICOM attributes without downloading full objects.
   •  How it works:
   ◦  HTTP GET requests with query params (e.g., PatientName, StudyDate, Modality).
   ◦  Returns JSON or XML with matching attributes for paging and selection.
   •  Typical endpoints:
   ◦  GET /studies
   ◦  GET /studies/{StudyInstanceUID}/series
   ◦  GET /studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances
   ◦  Supports “includefield” to control which tags are returned.

2) WADO-RS (Web Access to DICOM Objects – RESTful)
   •  Purpose: Retrieve actual DICOM instances and derived representations.
   •  How it works:
   ◦  HTTP GET for binary DICOM, rendered images (e.g., JPEG/PNG), or frames (for multi-frame).
   ◦  Content negotiation via Accept header (e.g., application/dicom; transfer-syntax=*, image/jpeg).
   •  Typical endpoints:
   ◦  GET /studies/{StudyInstanceUID}
   ◦  GET /studies/{StudyInstanceUID}/series/{SeriesInstanceUID}
   ◦  GET /studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/{SOPInstanceUID}
   ◦  GET …/instances/{SOPInstanceUID}/frames/{frameList} (for multi-frame)
   •  Also supports retrieving metadata only: …/metadata (returns JSON DICOM attributes).

3) STOW-RS (Store Over the Web)
   •  Purpose: Upload/store DICOM objects to a server (PACS/VNA).
   •  How it works:
   ◦  HTTP POST multipart/related with part(s) of type application/dicom.
   ◦  Returns a JSON/XML summary of what was stored (including failures).
   •  Typical endpoint:
   ◦  POST /studies (server creates or appends to studies based on tags in objects).

WADO-URI (legacy, for completeness)
•  Older URL-style fetch: GET with query parameters like requestType=WADO, studyUID=..., seriesUID=..., objectUID=...
•  Still supported by many servers, but WADO-RS is preferred today.

Common patterns when building a viewer
•  Discover via QIDO-RS:
◦  Search studies by date/modality/patient, then drill into series and instances.
•  Fetch metadata:
◦  Use WADO-RS metadata endpoints to build your viewport state efficiently (no full pixel data yet).
•  Fetch pixel data:
◦  For multi-frame or large instances, request specific frames as needed.
◦  Use transfer syntaxes your client supports; for compressed pixel data, make sure your decoding stack handles it or request server-side rendering (e.g., image/jpeg).
•  Thumbnails/previews:
◦  Use WADO-RS rendered endpoints (image/jpeg) for quick thumbnails if supported.
•  Upload:
◦  Use STOW-RS to push new instances (e.g., secondary captures, annotations encoded as DICOM SR, etc.).

Security and interoperability
•  Auth is typically OAuth2/OIDC or bearer tokens over HTTPS.
•  CORS matters for browser-based viewers; ensure the DICOMweb server sets appropriate headers.
•  Pay attention to Accept headers and character sets for attribute matching.

What is OHIF?
•  OHIF (Open Health Imaging Foundation) Viewer is an open-source, browser-based medical imaging viewer built on modern web tech.
•  Key traits:
◦  Uses Cornerstone libraries for image rendering/interaction (pan/zoom/WW/WC, MPR, etc.).
◦  Natively supports DICOMweb (QIDO/WADO/STOW), making it a good reference and a production-ready viewer.
◦  Extensible via a modular architecture: “extensions” and “modes” let you add tools (e.g., SR measurement display, seg overlays, RTSTRUCT, microscopy).
◦  Integrates with common backends like Orthanc, dcm4chee, Google Cloud Healthcare, AWS HealthImaging (via adapters).
◦  Offers study/series browser, measurements, hanging protocols, layout, viewport sync, cine, and more.

How OHIF fits into your build
•  As a full viewer: Deploy OHIF with a config that points to your DICOMweb server URLs and credentials.
•  As a reference: Study how OHIF queries (QIDO-RS) and retrieves (WADO-RS) data, handles metadata, and requests frames/representations. You can reuse the patterns or Cornerstone stack even if you don’t adopt OHIF wholesale.
•  As a platform: Write extensions for custom tools, workflows, or integrations (e.g., AI inference overlays, report links).

Minimal end-to-end flow example for a custom viewer
•  Search studies:
◦  GET /studies?PatientName=DOE^JOHN&includefield=all
•  List series for a chosen study:
◦  GET /studies/{StudyInstanceUID}/series?includefield=all
•  Get instance metadata for layout:
◦  GET /studies/{StudyInstanceUID}/series/{SeriesInstanceUID}/instances/metadata
•  Load a frame to display:
◦  GET …/instances/{SOPInstanceUID}/frames/1 with Accept: image/jpeg or application/dicom (depending on your renderer)
•  Store an object (e.g., derived SR):
◦  POST /studies with Content-Type: multipart/related; type="application/dicom"

Practical tips
•  Indexing: Some servers require pre-indexing for QIDO to be fast; verify server readiness.
•  Tag filters: Limit includefield to only what you need to reduce payload sizes and latency.
•  Transfer syntax: If you don’t support all codecs client-side, either request server-side transcoding (some servers support specifying transfer-syntax in Accept) or rendered images.
•  Frame access: For multi-frame modalities (CT, MR) use frame endpoints and range requests to improve performance.
•  Errors: Handle partial success for STOW and HTTP status codes (e.g., 204/200/202) carefully.
•  CORS and auth: Configure server to allow your web origin and attach tokens as Authorization: Bearer.
```

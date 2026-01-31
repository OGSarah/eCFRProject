# eCFR Project

## Purpose
The United States Federal Government has over 200,000 pages of federal regulations across ~150 main agencies, all of which can be found within the eCFR at https://www.ecfr.gov/. There is a public API for it.
The goal of this assessment is to create a simple website to analyze Federal Regulations to allow for more digestible and actionable insights to be made on potential deregulation efforts across the government.

## Project Requirements (from the prompt)
- Download the current eCFR data, store the data server-side, create APIs that can retrieve the stored data, and provide a UI to analyze it for items such as word count per agency, historical changes over time, and a checksum for each agency.
- Only implement analysis that provides meaningful information to the user.
- Add at least one custom metric that helps inform decision-making.
- Provide a way for users to review results.

## What This Project Implements
- Data ingestion from the eCFR API and storage in a local SQLite database and gzip-compressed XML snapshots.
- API endpoints for agencies, metrics, and refresh state.
- UI for reviewing agency metrics.
- Metrics implemented:
  - `word_count` (per agency)
  - `words_per_chapter` (per agency)
  - `checksum` (per agency)
  - `readability` (Flesch Reading Ease)
  - `churn` (custom metric): ratio of agency-referenced chapters whose content changed compared to the previous snapshot, best-effort based on available prior data.

## Local Setup
### Prerequisites
- Go 1.21+ (or your installed Go version that supports modules)

### Install Dependencies
```bash
go mod tidy
```

### Run the Server (and Website)
The server also serves the static website from `ecfr-analytics/web`.
```bash
cd ecfr-analytics
go run ./cmd/server
```

By default it listens on `http://localhost:8080`.

### Notes on First Run
- The server will kick off a background refresh on startup and then refresh daily.
- The initial download can take several minutes depending on network speed.
- Data is stored under `ecfr-analytics/data`.

### Useful Endpoints
- `GET /api/health`
- `POST /api/refresh`
- `GET /api/agencies`
- `GET /api/metrics/latest?metric=word_count|words_per_chapter|checksum|churn|readability`
- `GET /api/state?key=last_refresh`

## Screenshots (HTML embed)
Replace the `src` paths with your actual screenshot file locations.
```html
<img src="docs/screenshots/dashboard-1.png" alt="Agency metrics dashboard" width="900" />
<img src="docs/screenshots/dashboard-2.png" alt="Metric detail view" width="900" />
```

## Submission Checklist (from the prompt)
- Zip file containing source code.
- No more than 1,200 lines of code (excluding tests and auto-generated files).
- Ensure the zip file can be extracted properly before sending.
- Document including:
  - Feedback on the assignment (including how your expertise/skillsets fit this assessment).
  - Duration it took to complete.
  - Link to frontend, if applicable.
  - Screenshots of the UI.
- Submit via KiteWorks by the recruiter-provided deadline.

## Feedback (placeholder)
- Expertise/skill fit: TODO
- Duration: TODO
- Frontend link: TODO

# eCFR Project

## Purpose
The United States Federal Government has over 200,000 pages of federal regulations across ~150 main agencies, all of which can be found within the eCFR at https://www.ecfr.gov/. There is a public API for it.
The goal of this assessment is to create a simple website to analyze Federal Regulations to allow for more digestible and actionable insights to be made on potential deregulation efforts across the government.

## Project Requirements
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
- Go 1.25.6

### Install Dependencies
```bash
go mod tidy
```

### Build and Run the Server (and Website)
The server also serves the static website from `ecfr-analytics/web`.
```bash
cd ecfr-analytics
go build ./cmd/server
go run ./cmd/server
```

By default it listens on `http://localhost:8080`.

### Notes on First Run
- The server will kick off a background refresh on startup and then refresh daily.
- The initial download can take several minutes depending on network speed.
- Data is stored under `ecfr-analytics/data`.

## Screenshots
<img src="screenshots/darkMode.png" alt="Dark Mode" width="900"
<img src="screenshots/lightMode.png" alt="Light Mode" width="900"


## Feedback
- Expertise/skill fit: My background spans Go backends, REST/HTTP APIs, data processing, and security-focused delivery, alongside years of building user-facing products. This project aligns with my experience designing services, storing and analyzing data, and presenting it in clear UIs. I’ve shipped and maintained production systems, built automation and tests, and worked across Swift, Java, JavaScript, and Go—skills that map directly to delivering a clean, end-to-end analytics app.
- Duration: 8 hrs
- I added more than the required number of lines of code. I tried to limit it, but I wanted the project to look polished. 
- I wanted to add code comments, since it’s pretty common to do so in Go, but I didn’t want to increase the line count even further beyond the requirement.

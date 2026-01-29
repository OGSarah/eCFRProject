const API = (path) => path; // same-origin

let wcChart, churnChart, tsChart;

async function jget(path) {
  const res = await fetch(API(path));
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

async function jpost(path) {
  const res = await fetch(API(path), { method: "POST" });
  if (!res.ok) throw new Error(`${res.status} ${await res.text()}`);
  return res.json();
}

function ensureChart(canvasId, cfg, chartRefSetter) {
  const ctx = document.getElementById(canvasId);
  if (!ctx) return null;
  const chart = new Chart(ctx, cfg);
  chartRefSetter(chart);
  return chart;
}

function topN(rows, n = 20) {
  // rows: {name, value}
  return [...rows]
    .filter(r => typeof r.value === "number")
    .sort((a, b) => b.value - a.value)
    .slice(0, n);
}

async function loadAgencies() {
  const agencies = await jget("/api/agencies");
  const sel = document.getElementById("agencySelect");
  sel.innerHTML = "";
  for (const a of agencies) {
    const opt = document.createElement("option");
    opt.value = a.slug;
    opt.textContent = a.name;
    sel.appendChild(opt);
  }
  sel.addEventListener("change", () => loadTimeseries());
  document.getElementById("metricSelect").addEventListener("change", () => loadTimeseries());
  document.getElementById("daysSelect").addEventListener("change", () => loadTimeseries());
}

async function refresh() {
  const out = document.getElementById("refreshOut");
  out.textContent = "Running...";
  try {
    const r = await jpost("/api/refresh");
    out.textContent = JSON.stringify(r, null, 2);
    await loadLatestCharts();
    await loadTimeseries();
    await loadChecksums();
  } catch (e) {
    out.textContent = String(e);
  }
}

async function loadLatestCharts() {
  const wcRows = await jget("/api/metrics/latest?metric=word_count");
  const churnRows = await jget("/api/metrics/latest?metric=churn");

  const wcTop = topN(wcRows, 15);
  const churnTop = topN(churnRows, 15);

  const wcCfg = {
    type: "bar",
    data: {
      labels: wcTop.map(r => r.name),
      datasets: [{ label: "Word count", data: wcTop.map(r => r.value) }]
    },
    options: { responsive: true, plugins: { legend: { display: false } } }
  };

  const churnCfg = {
    type: "bar",
    data: {
      labels: churnTop.map(r => r.name),
      datasets: [{ label: "Churn rate", data: churnTop.map(r => r.value) }]
    },
    options: {
      responsive: true,
      scales: { y: { suggestedMin: 0, suggestedMax: 1 } },
      plugins: { legend: { display: false } }
    }
  };

  if (wcChart) wcChart.destroy();
  if (churnChart) churnChart.destroy();

  wcChart = new Chart(document.getElementById("wcChart"), wcCfg);
  churnChart = new Chart(document.getElementById("churnChart"), churnCfg);
}

async function loadTimeseries() {
  const slug = document.getElementById("agencySelect").value;
  if (!slug) return;

  const metric = document.getElementById("metricSelect").value;
  const days = document.getElementById("daysSelect").value;

  const rows = await jget(`/api/metrics/agency/${encodeURIComponent(slug)}/timeseries?metric=${encodeURIComponent(metric)}&days=${encodeURIComponent(days)}`);
  const data = [...rows].reverse(); // oldest -> newest

  const cfg = {
    type: "line",
    data: {
      labels: data.map(r => r.date),
      datasets: [{ label: metric, data: data.map(r => r.value) }]
    },
    options: { responsive: true, plugins: { legend: { display: true } } }
  };

  if (tsChart) tsChart.destroy();
  tsChart = new Chart(document.getElementById("tsChart"), cfg);

  document.getElementById("agencyMeta").textContent =
    `slug: ${slug} • points: ${data.length}`;
}

async function loadChecksums() {
  const rows = await jget("/api/metrics/latest?metric=checksum");
  const tbody = document.getElementById("checksumTable");
  tbody.innerHTML = "";
  for (const r of rows.slice(0, 50)) {
    const tr = document.createElement("tr");
    tr.innerHTML = `<td>${escapeHtml(r.name)}</td><td>${escapeHtml(r.date)}</td><td><code>${escapeHtml(String(r.value))}</code></td>`;
    tbody.appendChild(tr);
  }
}

function escapeHtml(s) {
  return s.replaceAll("&", "&amp;").replaceAll("<", "&lt;").replaceAll(">", "&gt;");
}

document.getElementById("refreshBtn").addEventListener("click", refresh);

(async function init() {
  await loadAgencies();
  await loadLatestCharts();
  await loadChecksums();
  await loadTimeseries();
})();

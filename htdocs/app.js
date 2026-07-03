(() => {
  const TOKEN_STORAGE_KEY = "image-detection.apiToken";

  const tokenForm = document.getElementById("token-form");
  const tokenInput = document.getElementById("token-input");

  const form = document.getElementById("upload-form");
  const fileInput = document.getElementById("file-input");
  const submitBtn = document.getElementById("submit-btn");
  const statusEl = document.getElementById("status");
  const previewSection = document.getElementById("preview-section");
  const previewImg = document.getElementById("preview");
  const previewVideo = document.getElementById("preview-video");
  const previewMeta = document.getElementById("preview-meta");
  const resultSection = document.getElementById("result-section");
  const summaryEl = document.getElementById("summary");
  const resultJson = document.getElementById("result-json");

  const logsList = document.getElementById("logs-list");
  const logsDetail = document.getElementById("logs-detail");
  const logsRefreshBtn = document.getElementById("logs-refresh-btn");

  // --- Token handling -------------------------------------------------
  // The bearer token is kept only in sessionStorage (cleared when the tab
  // closes) and is never written into this file or any served asset.

  function getToken() {
    return sessionStorage.getItem(TOKEN_STORAGE_KEY) || "";
  }

  function authHeaders() {
    const token = getToken();
    return token ? { Authorization: "Bearer " + token } : {};
  }

  // Pre-fill the token field with "test" (the default backend value) if
  // the user has not saved a token yet. If the backend API_BEARER_TOKEN
  // has been changed, replace this value with the one from the backend.
  tokenInput.value = getToken() || "test";
  if (!sessionStorage.getItem(TOKEN_STORAGE_KEY)) {
    sessionStorage.setItem(TOKEN_STORAGE_KEY, "test");
  }
  tokenForm.addEventListener("submit", (event) => {
    event.preventDefault();
    sessionStorage.setItem(TOKEN_STORAGE_KEY, tokenInput.value.trim());
    setStatus("success", "Token saved for this browser tab.");
    refreshLogs();
  });

  // --- Status helpers ---------------------------------------------------

  function setStatus(kind, message) {
    statusEl.hidden = false;
    statusEl.className = "status " + kind;
    statusEl.textContent = message;
  }

  function clearStatus() {
    statusEl.hidden = true;
    statusEl.textContent = "";
  }

  function renderLabelList(labels) {
    const list = document.createElement("ul");
    list.className = "label-list";
    for (const l of labels) {
      const li = document.createElement("li");
      li.textContent = `${l.label}${l.riskLevel ? " — " + l.riskLevel : ""}${
        l.confidence ? ` (${l.confidence}%)` : ""
      }${l.description ? " — " + l.description : ""}`;
      list.appendChild(li);
    }
    return list;
  }

  function renderSummary(kind, summary) {
    summaryEl.innerHTML = "";

    const verdict = document.createElement("p");
    verdict.className = "status " + (summary.passed ? "success" : "error");
    verdict.textContent = summary.passed
      ? `✓ Passed — risk level: ${summary.riskLevel || "none"}`
      : `⚠ Flagged — risk level: ${summary.riskLevel || "unknown"}`;
    summaryEl.appendChild(verdict);

    const msg = document.createElement("p");
    msg.className = "meta";
    msg.textContent = summary.message || "";
    summaryEl.appendChild(msg);

    if (kind === "video") {
      if (summary.frameLabels && summary.frameLabels.length > 0) {
        const h = document.createElement("p");
        h.className = "meta";
        h.textContent = "Visual (frame) findings:";
        summaryEl.appendChild(h);
        summaryEl.appendChild(renderLabelList(summary.frameLabels));
      }
      if (summary.audioLabels && summary.audioLabels.length > 0) {
        const h = document.createElement("p");
        h.className = "meta";
        h.textContent = "Audio findings:";
        summaryEl.appendChild(h);
        summaryEl.appendChild(renderLabelList(summary.audioLabels));
      }
    } else if (summary.labels && summary.labels.length > 0) {
      summaryEl.appendChild(renderLabelList(summary.labels));
    }
  }

  // --- Upload + moderate -------------------------------------------------

  async function processFile(file) {
    const body = new FormData();
    body.append("file", file);
    const res = await fetch("/api/process", { method: "POST", body, headers: authHeaders() });
    const data = await res.json().catch(() => ({}));
    if (!res.ok) {
      throw new Error(data.error || `request failed (${res.status})`);
    }
    return data; // { kind, resized, raw, summary }
  }

  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    clearStatus();
    resultSection.hidden = true;
    previewSection.hidden = true;
    previewImg.hidden = true;
    previewVideo.hidden = true;

    const file = fileInput.files[0];
    if (!file) {
      setStatus("error", "Please choose an image or video file first.");
      return;
    }
    if (!getToken()) {
      setStatus("error", "Please enter and save your API bearer token first.");
      return;
    }

    const isVideo = file.type.startsWith("video/");

    submitBtn.disabled = true;
    try {
      const objectUrl = URL.createObjectURL(file);
      if (isVideo) {
        previewVideo.src = objectUrl;
        previewVideo.hidden = false;
      } else {
        previewImg.src = objectUrl;
        previewImg.hidden = false;
      }
      previewSection.hidden = false;

      setStatus(
        "info",
        isVideo
          ? "Uploading video and running moderation… this can take a minute or more (asynchronous processing)."
          : "Uploading, moderating, and cleaning up…"
      );
      const result = await processFile(file);

      if (result.kind === "image") {
        previewMeta.textContent = result.resized
          ? "Image was automatically resized to meet moderation limits."
          : "Image processed as-is (within moderation limits).";
      } else {
        previewMeta.textContent = "Video processed via asynchronous moderation.";
      }

      renderSummary(result.kind, result.summary || {});
      resultJson.textContent = JSON.stringify(result.raw, null, 2);
      resultSection.hidden = false;
      setStatus("success", "Done. Temporary file has been deleted from storage.");
      refreshLogs();
    } catch (err) {
      setStatus("error", err.message || String(err));
    } finally {
      submitBtn.disabled = false;
    }
  });

  // --- Logs viewer ---------------------------------------------------

  async function refreshLogs() {
    logsList.innerHTML = "";
    logsDetail.hidden = true;
    if (!getToken()) return;

    try {
      const res = await fetch("/api/logs", { headers: authHeaders() });
      if (!res.ok) throw new Error(`failed to load logs (${res.status})`);
      const records = await res.json();

      if (!records || records.length === 0) {
        const li = document.createElement("li");
        li.textContent = "No calls logged yet.";
        logsList.appendChild(li);
        return;
      }

      for (const rec of records) {
        const li = document.createElement("li");
        const when = new Date(rec.timestamp).toLocaleString();
        li.textContent = `[${rec.status}] ${rec.kind} — ${rec.filename || "?"} — ${when}`;
        li.addEventListener("click", () => showLogDetail(rec.file));
        logsList.appendChild(li);
      }
    } catch (err) {
      const li = document.createElement("li");
      li.textContent = "Error loading logs: " + (err.message || err);
      logsList.appendChild(li);
    }
  }

  async function showLogDetail(file) {
    try {
      const res = await fetch("/api/logs/" + encodeURIComponent(file), { headers: authHeaders() });
      const data = await res.json();
      logsDetail.textContent = JSON.stringify(data, null, 2);
      logsDetail.hidden = false;
    } catch (err) {
      logsDetail.textContent = "Error: " + (err.message || err);
      logsDetail.hidden = false;
    }
  }

  logsRefreshBtn.addEventListener("click", refreshLogs);
  refreshLogs();
})();

// ONVIF PTZ Web Controller Client
let currentMode = "continuous"; // "continuous" (hold to move) or "step" (click to nudge)
let currentSpeed = 0.5;
let currentDuration = 0.35;
let activeInterval = null;
let isMoving = false;

document.addEventListener("DOMContentLoaded", () => {
  initPlayer();
  initControls();
  initPresets();
  initTelemetry();
});

// ─────────────────────────────────────────────────────────────
// 1. LIVE VIDEO PLAYER
// ─────────────────────────────────────────────────────────────
function initPlayer() {
  const video = document.getElementById("cameraVideo");
  const hlsUrl = "/hls/live/playlist.m3u8";

  if (Hls.isSupported()) {
    const hls = new Hls({
      enableWorker: true,
      lowLatencyMode: true,
      liveSyncDurationCount: 2,
      liveMaxLatencyDurationCount: 4,
    });
    hls.loadSource(hlsUrl);
    hls.attachMedia(video);
    hls.on(Hls.Events.MANIFEST_PARSED, () => {
      video.play().catch(() => {});
      document.getElementById("videoOverlay").style.display = "none";
    });
    hls.on(Hls.Events.ERROR, (event, data) => {
      if (data.fatal) {
        switch (data.type) {
          case Hls.ErrorTypes.NETWORK_ERROR:
            setTimeout(() => hls.loadSource(hlsUrl), 2000);
            break;
          case Hls.ErrorTypes.MEDIA_ERROR:
            hls.recoverMediaError();
            break;
          default:
            hls.destroy();
            break;
        }
      }
    });
  } else if (video.canPlayType("application/vnd.apple.mpegurl")) {
    video.src = hlsUrl;
    video.addEventListener("loadedmetadata", () => {
      video.play().catch(() => {});
      document.getElementById("videoOverlay").style.display = "none";
    });
  }
}

// ─────────────────────────────────────────────────────────────
// 2. PTZ CONTROLS (D-PAD & ZOOM)
// ─────────────────────────────────────────────────────────────
function initControls() {
  // Speed Slider
  const speedSlider = document.getElementById("speedSlider");
  const speedVal = document.getElementById("speedVal");
  speedSlider.addEventListener("input", (e) => {
    currentSpeed = parseFloat(e.target.value);
    speedVal.textContent = `${Math.round(currentSpeed * 100)}%`;
  });

  // Mode Switch
  document.querySelectorAll(".mode-btn").forEach((btn) => {
    btn.addEventListener("click", () => {
      document.querySelectorAll(".mode-btn").forEach((b) => b.classList.remove("active"));
      btn.classList.add("active");
      currentMode = btn.getAttribute("data-mode");
    });
  });

  // Bind 8-Way D-Pad buttons
  document.querySelectorAll(".dpad-btn[data-dir]").forEach((btn) => {
    const dir = btn.getAttribute("data-dir");
    bindPtzAction(btn, () => ptzStartMove(dir), () => ptzStop());
  });

  // Center Stop button
  document.getElementById("btnStop").addEventListener("click", () => ptzStop());

  // Zoom Buttons
  const btnZoomIn = document.getElementById("btnZoomIn");
  const btnZoomOut = document.getElementById("btnZoomOut");
  bindPtzAction(btnZoomIn, () => ptzStartZoom("in"), () => ptzStop());
  bindPtzAction(btnZoomOut, () => ptzStartZoom("out"), () => ptzStop());
}

function bindPtzAction(element, startFn, stopFn) {
  // Mouse Events
  element.addEventListener("mousedown", (e) => {
    e.preventDefault();
    if (currentMode === "continuous") {
      startFn();
    } else {
      // Step mode
      element.classList.add("active");
      startFn();
      setTimeout(() => element.classList.remove("active"), 300);
    }
  });

  element.addEventListener("mouseup", (e) => {
    e.preventDefault();
    if (currentMode === "continuous") stopFn();
  });

  element.addEventListener("mouseleave", (e) => {
    if (currentMode === "continuous" && isMoving) stopFn();
  });

  // Touch Events for Mobile / Tablet UI
  element.addEventListener("touchstart", (e) => {
    e.preventDefault();
    if (currentMode === "continuous") {
      startFn();
    } else {
      element.classList.add("active");
      startFn();
      setTimeout(() => element.classList.remove("active"), 300);
    }
  });

  element.addEventListener("touchend", (e) => {
    e.preventDefault();
    if (currentMode === "continuous") stopFn();
  });
}

async function ptzStartMove(direction) {
  isMoving = true;
  if (currentMode === "continuous") {
    try {
      await fetch("/api/ptz/move", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ direction: direction, speed: currentSpeed }),
      });
    } catch (e) {
      console.error("PTZ Move Error:", e);
    }
  } else {
    // Step Mode
    try {
      await fetch("/api/ptz/step", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ direction: direction, speed: currentSpeed, duration: currentDuration }),
      });
    } catch (e) {
      console.error("PTZ Step Error:", e);
    }
  }
}

async function ptzStartZoom(direction) {
  isMoving = true;
  if (currentMode === "continuous") {
    try {
      await fetch("/api/ptz/zoom", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ direction: direction, speed: currentSpeed }),
      });
    } catch (e) {
      console.error("PTZ Zoom Error:", e);
    }
  } else {
    try {
      await fetch("/api/ptz/zoom-step", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ direction: direction, speed: currentSpeed, duration: currentDuration }),
      });
    } catch (e) {
      console.error("PTZ Zoom Step Error:", e);
    }
  }
}

async function ptzStop() {
  isMoving = false;
  try {
    await fetch("/api/ptz/stop", { method: "POST" });
  } catch (e) {
    console.error("PTZ Stop Error:", e);
  }
}

// ─────────────────────────────────────────────────────────────
// 3. PRESET MANAGEMENT
// ─────────────────────────────────────────────────────────────
async function initPresets() {
  await loadPresets();

  document.getElementById("btnAddPreset").addEventListener("click", async () => {
    const input = document.getElementById("presetNameInput");
    const name = input.value.trim();
    if (!name) return;

    try {
      const res = await fetch("/api/ptz/presets/set", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ preset_name: name }),
      });
      if (res.ok) {
        input.value = "";
        await loadPresets();
      }
    } catch (e) {
      console.error("Set Preset Error:", e);
    }
  });
}

async function loadPresets() {
  const listEl = document.getElementById("presetList");
  try {
    const res = await fetch("/api/ptz/presets");
    const data = await res.json();
    const presets = data.presets || [];

    if (presets.length === 0) {
      listEl.innerHTML = '<div style="color: var(--text-muted); font-size: 0.8rem; text-align: center; padding: 10px;">No presets saved yet.</div>';
      return;
    }

    listEl.innerHTML = presets
      .map(
        (p) => `
      <div class="preset-item">
        <span>📍 ${escapeHtml(p.name)}</span>
        <div class="preset-actions">
          <button class="btn-small goto" onclick="gotoPreset('${p.token}')">Goto</button>
          <button class="btn-small del" onclick="deletePreset('${p.token}')">✕</button>
        </div>
      </div>
    `
      )
      .join("");
  } catch (e) {
    listEl.innerHTML = '<div style="color: var(--danger); font-size: 0.8rem;">Failed to load presets.</div>';
  }
}

window.gotoPreset = async function (token) {
  try {
    await fetch("/api/ptz/presets/goto", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ preset_token: token, speed: 1.0 }),
    });
  } catch (e) {
    console.error("Goto Preset Error:", e);
  }
};

window.deletePreset = async function (token) {
  try {
    await fetch(`/api/ptz/presets/${token}`, { method: "DELETE" });
    await loadPresets();
  } catch (e) {
    console.error("Delete Preset Error:", e);
  }
};

// ─────────────────────────────────────────────────────────────
// 4. TELEMETRY & STATUS POLLING
// ─────────────────────────────────────────────────────────────
function initTelemetry() {
  async function pollStatus() {
    try {
      const res = await fetch("/api/ptz/status");
      const data = await res.json();
      if (data.status === "online") {
        document.getElementById("telemetryPan").textContent = data.pan.toFixed(2);
        document.getElementById("telemetryTilt").textContent = data.tilt.toFixed(2);
        document.getElementById("telemetryZoom").textContent = data.zoom.toFixed(2);
      }
    } catch (e) {}
  }
  setInterval(pollStatus, 1500);
}

// ─────────────────────────────────────────────────────────────
// 5. API MODAL
// ─────────────────────────────────────────────────────────────
window.showApiModal = function () {
  document.getElementById("apiModal").style.display = "flex";
};

window.closeApiModal = function () {
  document.getElementById("apiModal").style.display = "none";
};

function escapeHtml(str) {
  return str.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
}

const state = {
  status: null,
  activeTab: "overview",
  selectedOverviewDrive: "",
  diffPreset: "24h",
  selectedTagFilter: "",
  lastDiff: null,
  explorerDrive: "",
  explorerSnapId: 0,
  explorerPath: "",
};

const TAG_LABELS = {
  "docker": "Docker / WSL",
  "ai-models": "Model AI / Cache ML",
  "dev-deps": "Thư viện dự án",
  "pkg-cache": "Cache gói",
  "temp-logs": "File tạm & Log",
  "downloads-media": "Tải về & Media",
  "vcs": "Lịch sử Git",
  "windows-system": "Hệ thống Windows",
  "other": "Khác",
};

const RULE_LABELS = {
  "low_space": "Ổ sắp đầy",
  "sudden_spike": "Tăng đột biến",
  "creeping_growth": "Tăng âm thầm",
  "predicted_full": "Dự báo đầy ổ",
};

function fmtBytes(b) {
  if (b === null || b === undefined) return "0 B";
  const neg = b < 0;
  let v = Math.abs(b);
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let idx = 0;
  while (v >= 1024 && idx < units.length - 1) {
    v /= 1024;
    idx++;
  }
  const str = idx === 0 ? `${Math.round(v)} ${units[idx]}` : `${v.toFixed(1)} ${units[idx]}`;
  return neg ? `-${str}` : str;
}

function fmtSigned(b) {
  if (!b) return "0 B";
  return b > 0 ? `+${fmtBytes(b)}` : fmtBytes(b);
}

function fmtDate(iso) {
  if (!iso) return "—";
  const d = new Date(iso);
  return d.toLocaleString("vi-VN", {
    day: "2-digit", month: "2-digit", year: "numeric",
    hour: "2-digit", minute: "2-digit",
  });
}

function esc(s) {
  return String(s ?? "")
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
    .replace(/"/g, "&quot;");
}

async function api(url, opts = {}) {
  const res = await fetch(url, opts);
  const data = await res.json();
  if (!res.ok) throw new Error(data.error || "API error");
  return data;
}

function switchTab(tab) {
  state.activeTab = tab;
  document.querySelectorAll(".nav-btn").forEach((b) => {
    b.classList.toggle("active", b.dataset.tab === tab);
  });
  document.querySelectorAll(".tab-panel").forEach((p) => {
    p.classList.toggle("active", p.id === `tab-${tab}`);
  });
  if (tab === "diff") loadDiffTab();
  if (tab === "explorer") loadExplorerTab();
  if (tab === "alerts") loadAlertsTab();
  if (tab === "settings") loadSettingsTab();
}

async function loadStatus() {
  const data = await api("/api/status");
  state.status = data;
  renderScanBanner(data.progress);
  renderAlertBadge(data.unread_alerts);

  const drives = data.drives || [];
  if (!state.selectedOverviewDrive && drives.length > 0) {
    state.selectedOverviewDrive = drives[0].drive.root;
  }
  if (!state.explorerDrive && drives.length > 0) {
    state.explorerDrive = drives[0].drive.root;
  }

  renderDriveCards(drives);
  renderOverviewDriveSelect(drives);
  renderHistoryChart(drives);
  await loadOverviewAlerts();
}

function renderScanBanner(progress) {
  const banner = document.getElementById("scan-banner");
  const btnAll = document.getElementById("btn-scan-all");
  const sideStatus = document.getElementById("sidebar-status");
  if (progress && progress.active) {
    banner.classList.remove("hidden");
    btnAll.disabled = true;
    const driveLabel = progress.drive_root || "các ổ đĩa";
    document.getElementById("scan-banner-title").textContent =
      `Đang quét ${driveLabel} — ${progress.dirs_scanned.toLocaleString("vi-VN")} thư mục, ${progress.files_scanned.toLocaleString("vi-VN")} file (${fmtBytes(progress.bytes_scanned)})`;
    document.getElementById("scan-banner-path").textContent = progress.current_path || "";
    sideStatus.textContent = "Đang quét...";
  } else {
    banner.classList.add("hidden");
    btnAll.disabled = false;
    const paused = state.status?.config?.paused;
    sideStatus.textContent = paused ? "Đang tạm dừng lịch" : "Đang chạy nền";
  }
}

function renderAlertBadge(count) {
  const badge = document.getElementById("alert-badge");
  if (count > 0) {
    badge.textContent = String(count);
    badge.classList.remove("hidden");
  } else {
    badge.classList.add("hidden");
  }
}

function renderDriveCards(drives) {
  const container = document.getElementById("drive-cards");
  if (!drives.length) {
    container.innerHTML = `<div class="card">Không tìm thấy ổ đĩa cố định nào.</div>`;
    return;
  }
  container.innerHTML = drives.map((ov) => {
    const d = ov.drive;
    const usedPct = d.total_bytes > 0 ? (d.used_bytes / d.total_bytes) * 100 : 0;
    let barColor = "var(--accent)";
    if (usedPct >= 92) barColor = "var(--red)";
    else if (usedPct >= 85) barColor = "var(--amber)";

    const deltaClass = ov.delta_24h_bytes > 0 ? "pill-up" : ov.delta_24h_bytes < 0 ? "pill-down" : "";
    const deltaText = ov.delta_24h_bytes === 0 ? "Ổn định 24h" : `${fmtSigned(ov.delta_24h_bytes)} / 24h`;

    let forecastPill = `<span class="pill">Chưa đủ dữ liệu dự báo</span>`;
    if (ov.forecast && ov.forecast.has_enough_data) {
      if (ov.forecast.daily_growth_bytes > 1024 * 1024 && ov.forecast.days_until_full > 0) {
        const days = Math.max(1, Math.ceil(ov.forecast.days_until_full));
        const cls = days <= 14 ? "pill-warn" : "pill-info";
        forecastPill = `<span class="pill ${cls}">~${days} ngày nữa đầy (${fmtSigned(ov.forecast.daily_growth_bytes)}/ngày)</span>`;
      } else {
        forecastPill = `<span class="pill pill-down">Xu hướng ổn định (${fmtSigned(ov.forecast.daily_growth_bytes)}/ngày)</span>`;
      }
    }

    const lastScan = ov.latest_snapshot ? fmtDate(ov.latest_snapshot.created_at) : "Chưa quét lần nào";

    return `
      <div class="drive-card">
        <div class="drive-top">
          <div>
            <div class="drive-title">${esc(d.root)} <span class="muted" style="font-weight:400;font-size:13px;">(${esc(d.label)})</span></div>
            <div class="drive-sub">Quét gần nhất: ${esc(lastScan)}</div>
          </div>
          <button class="btn btn-ghost btn-sm" data-action="scan-drive" data-drive="${esc(d.root)}">Quét</button>
        </div>
        <div class="progress-track">
          <div class="progress-fill" style="width:${usedPct.toFixed(1)}%;background:${barColor}"></div>
        </div>
        <div class="drive-meta">
          <span>Đã dùng: <strong>${fmtBytes(d.used_bytes)}</strong> (${usedPct.toFixed(1)}%)</span>
          <span>Trống: <strong>${fmtBytes(d.free_bytes)}</strong> / ${fmtBytes(d.total_bytes)}</span>
        </div>
        <div class="drive-badges">
          <span class="pill ${deltaClass}">${deltaText}</span>
          ${forecastPill}
        </div>
      </div>
    `;
  }).join("");
}

function renderOverviewDriveSelect(drives) {
  const sel = document.getElementById("overview-drive-select");
  sel.innerHTML = drives.map((ov) =>
    `<option value="${esc(ov.drive.root)}" ${ov.drive.root === state.selectedOverviewDrive ? "selected" : ""}>${esc(ov.drive.root)} (${esc(ov.drive.label)})</option>`
  ).join("");
}

function renderHistoryChart(drives) {
  const wrap = document.getElementById("history-chart");
  const ov = drives.find((x) => x.drive.root === state.selectedOverviewDrive) || drives[0];
  const hist = ov?.history || [];
  if (hist.length < 2) {
    wrap.innerHTML = `<p class="muted">Cần ít nhất 2 snapshot để vẽ biểu đồ xu hướng theo thời gian. Bấm <strong>Quét</strong> hoặc đợi chu kỳ quét tiếp theo.</p>`;
    return;
  }

  const W = 820, H = 220, padL = 62, padR = 20, padT = 18, padB = 34;
  const vals = hist.map((s) => s.drive_used_bytes);
  let minV = Math.min(...vals);
  let maxV = Math.max(...vals);
  if (maxV === minV) {
    minV = Math.max(0, minV - 1024 * 1024 * 1024);
    maxV += 1024 * 1024 * 1024;
  } else {
    const pad = (maxV - minV) * 0.15;
    minV = Math.max(0, minV - pad);
    maxV += pad;
  }

  const plotW = W - padL - padR;
  const plotH = H - padT - padB;
  const coords = hist.map((s, i) => {
    const x = padL + (i / (hist.length - 1)) * plotW;
    const y = padT + (1 - (s.drive_used_bytes - minV) / (maxV - minV)) * plotH;
    return { x, y, snap: s };
  });

  const linePoints = coords.map((c) => `${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ");
  const areaPath = `M ${coords[0].x.toFixed(1)},${(padT + plotH).toFixed(1)} ` +
    coords.map((c) => `L ${c.x.toFixed(1)},${c.y.toFixed(1)}`).join(" ") +
    ` L ${coords[coords.length - 1].x.toFixed(1)},${(padT + plotH).toFixed(1)} Z`;

  const gridLines = [0, 0.5, 1].map((t) => {
    const y = padT + t * plotH;
    const val = maxV - t * (maxV - minV);
    return `
      <line x1="${padL}" y1="${y}" x2="${W - padR}" y2="${y}" stroke="#26334a" stroke-dasharray="3,3" />
      <text x="${padL - 8}" y="${y + 4}" fill="#94a3b8" font-size="11" text-anchor="end">${fmtBytes(val)}</text>
    `;
  }).join("");

  const dots = coords.map((c) => `
    <circle cx="${c.x.toFixed(1)}" cy="${c.y.toFixed(1)}" r="4" fill="#38bdf8" stroke="#0b1120" stroke-width="1.5">
      <title>${esc(fmtDate(c.snap.created_at))}: Đã dùng ${fmtBytes(c.snap.drive_used_bytes)} (Trống ${fmtBytes(c.snap.drive_free_bytes)})</title>
    </circle>
  `).join("");

  const firstLabel = fmtDate(hist[0].created_at);
  const lastLabel = fmtDate(hist[hist.length - 1].created_at);

  wrap.innerHTML = `
    <svg viewBox="0 0 ${W} ${H}" class="chart-svg">
      <defs>
        <linearGradient id="areaGrad" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stop-color="#38bdf8" stop-opacity="0.35" />
          <stop offset="100%" stop-color="#38bdf8" stop-opacity="0.02" />
        </linearGradient>
      </defs>
      ${gridLines}
      <path d="${areaPath}" fill="url(#areaGrad)" />
      <polyline points="${linePoints}" fill="none" stroke="#38bdf8" stroke-width="2.5" />
      ${dots}
      <text x="${padL}" y="${H - 8}" fill="#94a3b8" font-size="11">${esc(firstLabel)}</text>
      <text x="${W - padR}" y="${H - 8}" fill="#94a3b8" font-size="11" text-anchor="end">${esc(lastLabel)}</text>
    </svg>
  `;
}

async function loadOverviewAlerts() {
  const data = await api("/api/alerts?unread=true");
  const list = data.alerts || [];
  const el = document.getElementById("overview-alerts-list");
  if (!list.length) {
    el.innerHTML = `<p class="muted">Không có cảnh báo chưa đọc nào. Hệ thống đang ổn định.</p>`;
    return;
  }
  el.innerHTML = renderAlertsMarkup(list.slice(0, 5));
}

async function loadDiffTab() {
  const drives = state.status?.drives || [];
  const driveSel = document.getElementById("diff-drive");
  if (!driveSel.options.length && drives.length) {
    driveSel.innerHTML = drives.map((d) => `<option value="${esc(d.drive.root)}">${esc(d.drive.root)}</option>`).join("");
  }
  const drive = driveSel.value || (drives[0]?.drive.root || "");
  if (!drive) return;

  const snapData = await api(`/api/snapshots?drive=${encodeURIComponent(drive)}&limit=100`);
  const snaps = snapData.snapshots || [];
  populateDiffSelects(snaps);

  await runDiffWithPreset(drive, state.diffPreset);
}

function populateDiffSelects(snaps) {
  const selA = document.getElementById("diff-snap-a");
  const selB = document.getElementById("diff-snap-b");
  const opts = snaps.map((s) =>
    `<option value="${s.id}">${esc(fmtDate(s.created_at))} (Dùng ${fmtBytes(s.drive_used_bytes)})</option>`
  ).join("");
  selA.innerHTML = opts;
  selB.innerHTML = opts;
  if (snaps.length >= 2) {
    selB.selectedIndex = 0;
    selA.selectedIndex = 1;
  }
}

async function runDiffWithPreset(drive, preset) {
  const data = await api(`/api/diff?drive=${encodeURIComponent(drive)}&preset=${encodeURIComponent(preset)}`);
  renderDiffResponse(data);
}

async function runDiffCustom() {
  const drive = document.getElementById("diff-drive").value;
  const a = document.getElementById("diff-snap-a").value;
  const b = document.getElementById("diff-snap-b").value;
  if (!a || !b) return;
  const data = await api(`/api/diff?drive=${encodeURIComponent(drive)}&a=${a}&b=${b}`);
  renderDiffResponse(data);
}

function renderDiffResponse(data) {
  const container = document.getElementById("diff-content");
  if (data.needs_more_snapshots) {
    container.innerHTML = `
      <div class="card">
        <h3>Chưa đủ snapshot để so sánh</h3>
        <p class="muted">Hiện mới có ${data.snapshot_count || 0} snapshot cho ổ này. Cần tối thiểu 2 lần quét ở các thời điểm khác nhau để tính chênh lệch.</p>
      </div>`;
    return;
  }
  state.lastDiff = data.diff;
  state.selectedTagFilter = "";
  renderDiffDetails();
}

function renderDiffDetails() {
  const d = state.lastDiff;
  if (!d) return;
  const container = document.getElementById("diff-content");

  const deltaClass = d.drive_used_delta > 0 ? "color:var(--red)" : d.drive_used_delta < 0 ? "color:var(--green)" : "";
  const tags = d.tag_breakdown || [];
  const filteredRoots = (d.root_causes || []).filter((x) => !state.selectedTagFilter || x.tag === state.selectedTagFilter);

  const tagChips = tags.length ? `
    <div class="tag-filter-bar">
      <button class="tag-chip ${!state.selectedTagFilter ? "active" : ""}" data-action="diff-tag" data-tag="">Tất cả nhóm</button>
      ${tags.map((t) => `
        <button class="tag-chip ${state.selectedTagFilter === t.tag ? "active" : ""}" data-action="diff-tag" data-tag="${esc(t.tag)}">
          ${esc(t.label)}: <strong>${fmtSigned(t.delta_bytes)}</strong> (${t.node_count})
        </button>
      `).join("")}
    </div>` : "";

  const rootRows = filteredRoots.length ? filteredRoots.map((rc) => `
    <tr>
      <td class="path-cell">
        <div class="mono">${esc(rc.path)}</div>
        <div class="hint-text">${esc(rc.hint)}</div>
      </td>
      <td><span class="pill">${esc(TAG_LABELS[rc.tag] || rc.tag)}</span></td>
      <td class="num">${fmtBytes(rc.size_before)} → ${fmtBytes(rc.size_after)}</td>
      <td class="num" style="color:var(--red);font-weight:700;">+${fmtBytes(rc.attributed_delta_bytes)}</td>
      <td class="num"><button class="btn btn-ghost btn-sm" data-action="open-explorer" data-path="${esc(rc.path)}">Mở Explorer</button></td>
    </tr>
  `).join("") : `<tr><td colspan="5" class="muted">Không có thư mục nào tăng đáng kể (>= 10 MB) trong khoảng thời gian này.</td></tr>`;

  const freedRows = (d.freed_spaces || []).slice(0, 15).map((f) => `
    <tr>
      <td class="path-cell mono">${esc(f.path)}</td>
      <td class="num">${fmtBytes(f.size_before)} → ${fmtBytes(f.size_after)}</td>
      <td class="num" style="color:var(--green);font-weight:700;">${fmtBytes(f.attributed_delta_bytes)}</td>
    </tr>
  `).join("");

  const largeFileRows = (d.large_file_diffs || []).slice(0, 20).map((lf) => `
    <tr>
      <td class="path-cell mono">${esc(lf.path)}</td>
      <td><span class="pill">${esc(TAG_LABELS[lf.tag] || lf.tag)}</span></td>
      <td class="num">${fmtBytes(lf.size_before)} → ${fmtBytes(lf.size_after)}</td>
      <td class="num" style="color:${lf.delta_bytes >= 0 ? "var(--red)" : "var(--green)"};font-weight:700;">${fmtSigned(lf.delta_bytes)}</td>
      <td class="num"><button class="btn btn-ghost btn-sm" data-action="open-explorer" data-path="${esc(lf.path)}">Mở</button></td>
    </tr>
  `).join("");

  container.innerHTML = `
    <div class="summary-strip">
      <div class="stat-box">
        <div class="stat-label">Chênh lệch toàn ổ đĩa</div>
        <div class="stat-value" style="${deltaClass}">${fmtSigned(d.drive_used_delta)}</div>
      </div>
      <div class="stat-box">
        <div class="stat-label">Chênh lệch dữ liệu quét được</div>
        <div class="stat-value">${fmtSigned(d.scanned_delta)}</div>
      </div>
      <div class="stat-box">
        <div class="stat-label">Mốc A → Mốc B</div>
        <div class="stat-value" style="font-size:13px;">${esc(fmtDate(d.snapshot_a.created_at))} → ${esc(fmtDate(d.snapshot_b.created_at))}</div>
      </div>
    </div>

    <div class="card">
      <div class="card-header">
        <div>
          <h2>Thư mục "thủ phạm" làm tăng dung lượng (Root-Cause)</h2>
          <p class="muted">Đã loại trừ phần tăng trùng lặp từ thư mục cha để chỉ thẳng nhánh gốc gây phình dung lượng.</p>
        </div>
      </div>
      ${tagChips}
      <div class="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Đường dẫn &amp; Gợi ý</th>
              <th>Phân loại</th>
              <th class="num">Trước → Sau</th>
              <th class="num">Tăng ròng</th>
              <th class="num">Thao tác</th>
            </tr>
          </thead>
          <tbody>${rootRows}</tbody>
        </table>
      </div>
    </div>

    ${freedRows ? `
      <div class="card">
        <div class="card-header"><h2>Nơi đã giải phóng dung lượng</h2></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>Thư mục</th><th class="num">Trước → Sau</th><th class="num">Giảm</th></tr></thead>
            <tbody>${freedRows}</tbody>
          </table>
        </div>
      </div>` : ""}

    ${largeFileRows ? `
      <div class="card">
        <div class="card-header"><h2>File lớn mới xuất hiện hoặc thay đổi kích thước</h2></div>
        <div class="table-wrap">
          <table>
            <thead><tr><th>File</th><th>Nhóm</th><th class="num">Trước → Sau</th><th class="num">Chênh lệch</th><th class="num">Thao tác</th></tr></thead>
            <tbody>${largeFileRows}</tbody>
          </table>
        </div>
      </div>` : ""}
  `;
}

window.setDiffTagFilter = function (tag) {
  state.selectedTagFilter = tag;
  renderDiffDetails();
};

async function loadExplorerTab() {
  const drives = state.status?.drives || [];
  const sel = document.getElementById("explorer-drive");
  if (!sel.options.length && drives.length) {
    sel.innerHTML = drives.map((d) => `<option value="${esc(d.drive.root)}">${esc(d.drive.root)}</option>`).join("");
  }
  const drive = sel.value || state.explorerDrive;
  if (!drive) return;
  state.explorerDrive = drive;

  const snapData = await api(`/api/snapshots?drive=${encodeURIComponent(drive)}&limit=1`);
  const snaps = snapData.snapshots || [];
  if (!snaps.length) {
    document.getElementById("explorer-tree").innerHTML = `<p class="muted">Chưa có snapshot cho ổ ${esc(drive)}.</p>`;
    document.getElementById("large-files-list").innerHTML = "";
    return;
  }
  state.explorerSnapId = snaps[0].id;
  if (!state.explorerPath || !state.explorerPath.toUpperCase().startsWith(drive.toUpperCase())) {
    state.explorerPath = drive;
  }
  await Promise.all([
    loadExplorerChildren(state.explorerSnapId, state.explorerPath),
    loadLargeFiles(state.explorerSnapId),
  ]);
}

async function loadExplorerChildren(snapId, parentPath) {
  state.explorerPath = parentPath;
  renderBreadcrumbs(parentPath);
  const data = await api(`/api/snapshot-children?snapshot_id=${snapId}&path=${encodeURIComponent(parentPath)}`);
  const children = data.children || [];
  const treeEl = document.getElementById("explorer-tree");
  if (!children.length) {
    treeEl.innerHTML = `<p class="muted">Không có thư mục con nào được lưu ở mức chi tiết này (các thư mục nhỏ hơn ngưỡng đã được gộp ở thư mục cha).</p>`;
    return;
  }
  treeEl.innerHTML = children.map((c) => `
    <div class="tree-row">
      <div class="tree-name mono" data-action="drill-explorer" data-path="${esc(c.path)}">${esc(c.name)}</div>
      <div><span class="pill">${esc(TAG_LABELS[c.tag] || c.tag)}</span></div>
      <div class="num"><strong>${fmtBytes(c.size_bytes)}</strong></div>
      <div class="num"><button class="btn btn-ghost btn-sm" data-action="open-explorer" data-path="${esc(c.path)}">Mở</button></div>
    </div>
  `).join("");
}

function renderBreadcrumbs(path) {
  const box = document.getElementById("explorer-breadcrumbs");
  const clean = path.replace(/\\+$/, "");
  const parts = clean.split("\\").filter(Boolean);
  let accum = "";
  const crumbs = parts.map((p, idx) => {
    accum = idx === 0 ? `${p}\\` : `${accum}${accum.endsWith("\\") ? "" : "\\"}${p}`;
    const target = accum;
    return `<button class="crumb mono" data-action="drill-explorer" data-path="${esc(target)}">${esc(idx === 0 ? target : p)}</button>`;
  });
  box.innerHTML = crumbs.join(`<span class="muted">/</span>`);
}

window.drillExplorer = function (path) {
  loadExplorerChildren(state.explorerSnapId, path);
};

async function loadLargeFiles(snapId) {
  const data = await api(`/api/snapshot-large-files?snapshot_id=${snapId}`);
  const files = data.files || [];
  const el = document.getElementById("large-files-list");
  if (!files.length) {
    el.innerHTML = `<p class="muted">Không có file lớn nào vượt ngưỡng cấu hình.</p>`;
    return;
  }
  el.innerHTML = `
    <div class="table-wrap">
      <table>
        <thead><tr><th>Đường dẫn</th><th>Nhóm</th><th class="num">Dung lượng</th><th class="num">Thao tác</th></tr></thead>
        <tbody>
          ${files.map((f) => `
            <tr>
              <td class="path-cell mono">${esc(f.path)}</td>
              <td><span class="pill">${esc(TAG_LABELS[f.tag] || f.tag)}</span></td>
              <td class="num"><strong>${fmtBytes(f.size_bytes)}</strong></td>
              <td class="num"><button class="btn btn-ghost btn-sm" data-action="open-explorer" data-path="${esc(f.path)}">Mở</button></td>
            </tr>
          `).join("")}
        </tbody>
      </table>
    </div>
  `;
}

function renderAlertsMarkup(alerts) {
  return alerts.map((a) => {
    const sevClass = a.severity === "critical" ? "pill-up" : "pill-warn";
    return `
      <div class="alert-item ${a.acknowledged ? "ack" : ""}">
        <div>
          <div style="display:flex;gap:8px;align-items:center;margin-bottom:4px;">
            <span class="pill ${sevClass}">${esc(a.severity.toUpperCase())}</span>
            <span class="pill">${esc(RULE_LABELS[a.rule_type] || a.rule_type)}</span>
            <span class="muted" style="font-size:12px;">${esc(fmtDate(a.created_at))}</span>
          </div>
          <div style="font-weight:600;">${esc(a.message)}</div>
          <div class="mono muted" style="margin-top:2px;">${esc(a.target_path)}</div>
        </div>
        <div style="display:flex;gap:6px;">
          <button class="btn btn-ghost btn-sm" data-action="open-explorer" data-path="${esc(a.target_path)}">Mở Explorer</button>
          ${a.acknowledged ? "" : `<button class="btn btn-ghost btn-sm" data-action="ack-alert" data-id="${a.id}">Đã xem</button>`}
        </div>
      </div>
    `;
  }).join("");
}

async function loadAlertsTab() {
  const unread = document.getElementById("alerts-unread-only").checked;
  const data = await api(`/api/alerts?unread=${unread ? "true" : "false"}`);
  const list = data.alerts || [];
  const el = document.getElementById("alerts-full-list");
  if (!list.length) {
    el.innerHTML = `<p class="muted">Không có cảnh báo nào.</p>`;
    return;
  }
  el.innerHTML = renderAlertsMarkup(list);
}

async function loadSettingsTab() {
  const cfg = await api("/api/config");
  const avail = state.status?.available_drives || [];
  const selectedSet = new Set((cfg.drives || []).map((d) => d.toUpperCase()));

  const box = document.getElementById("settings-drives-box");
  box.innerHTML = avail.map((d) => {
    const checked = selectedSet.size === 0 || selectedSet.has(d.root.toUpperCase());
    return `<label class="checkbox-inline"><input type="checkbox" name="cfg-drive" value="${esc(d.root)}" ${checked ? "checked" : ""} /> ${esc(d.root)} (${esc(d.label)})</label>`;
  }).join("");

  document.getElementById("cfg-interval").value = cfg.scan_interval_minutes;
  document.getElementById("cfg-maxdepth").value = cfg.max_depth;
  document.getElementById("cfg-minpersist-mb").value = Math.round(cfg.min_persist_bytes / (1024 * 1024));
  document.getElementById("cfg-workers").value = cfg.scanner_workers;
  document.getElementById("cfg-autostart").checked = !!cfg.auto_start;
  document.getElementById("cfg-paused").checked = !!cfg.paused;
  document.getElementById("cfg-ignores").value = (cfg.ignore_paths || []).join("\n");

  const a = cfg.alerts;
  document.getElementById("cfg-low-pct").value = a.low_space_percent;
  document.getElementById("cfg-low-gb").value = a.low_space_gb;
  document.getElementById("cfg-crit-pct").value = a.critical_space_percent;
  document.getElementById("cfg-crit-gb").value = a.critical_space_gb;
  document.getElementById("cfg-spike-gb").value = (a.spike_bytes / (1024 * 1024 * 1024)).toFixed(1);
  document.getElementById("cfg-creep-runs").value = a.creeping_min_runs;
  document.getElementById("cfg-creep-gb").value = (a.creeping_min_bytes / (1024 * 1024 * 1024)).toFixed(1);
  document.getElementById("cfg-forecast-days").value = a.forecast_days_warn;
  document.getElementById("cfg-cooldown").value = a.cooldown_hours;
  document.getElementById("cfg-toast").checked = !!a.enable_toast;
  document.getElementById("cfg-webhook").value = a.webhook_url || "";
  document.getElementById("cfg-tg-token").value = a.telegram_bot_token || "";
  document.getElementById("cfg-tg-chat").value = a.telegram_chat_id || "";
}

window.triggerScan = async function (drive) {
  await api("/api/scan", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ drive }),
  });
  await loadStatus();
};

window.openExplorer = async function (path) {
  try {
    await api("/api/open-explorer", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ path }),
    });
  } catch (err) {
    alert("Không thể mở đường dẫn: " + err.message);
  }
};

window.ackAlert = async function (id) {
  await api("/api/alerts/ack", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ id }),
  });
  await loadStatus();
  if (state.activeTab === "alerts") await loadAlertsTab();
};

document.querySelectorAll(".nav-btn").forEach((btn) => {
  btn.addEventListener("click", () => switchTab(btn.dataset.tab));
});

document.getElementById("btn-scan-all").addEventListener("click", () => window.triggerScan(""));
document.getElementById("btn-refresh").addEventListener("click", () => loadStatus());
document.getElementById("btn-overview-alerts").addEventListener("click", () => switchTab("alerts"));

document.getElementById("overview-drive-select").addEventListener("change", (e) => {
  state.selectedOverviewDrive = e.target.value;
  renderHistoryChart(state.status?.drives || []);
});

document.querySelectorAll(".preset-btn").forEach((btn) => {
  btn.addEventListener("click", () => {
    document.querySelectorAll(".preset-btn").forEach((b) => b.classList.remove("active"));
    btn.classList.add("active");
    state.diffPreset = btn.dataset.preset;
    const drive = document.getElementById("diff-drive").value;
    runDiffWithPreset(drive, state.diffPreset);
  });
});

document.getElementById("diff-drive").addEventListener("change", () => loadDiffTab());
document.getElementById("btn-run-diff").addEventListener("click", () => runDiffCustom());
document.getElementById("explorer-drive").addEventListener("change", (e) => {
  state.explorerDrive = e.target.value;
  state.explorerPath = e.target.value;
  loadExplorerTab();
});
document.getElementById("btn-open-current").addEventListener("click", () => window.openExplorer(state.explorerPath));

document.getElementById("alerts-unread-only").addEventListener("change", () => loadAlertsTab());
document.getElementById("btn-ack-all").addEventListener("click", async () => {
  await api("/api/alerts/ack-all", { method: "POST" });
  await loadStatus();
  await loadAlertsTab();
});

document.getElementById("settings-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const base = await api("/api/config");
  const driveChecks = Array.from(document.querySelectorAll('input[name="cfg-drive"]:checked')).map((el) => el.value);
  const availCount = document.querySelectorAll('input[name="cfg-drive"]').length;
  const gib = 1024 * 1024 * 1024;
  const mib = 1024 * 1024;

  const payload = {
    ...base,
    drives: driveChecks.length === availCount ? [] : driveChecks,
    scan_interval_minutes: Number(document.getElementById("cfg-interval").value),
    max_depth: Number(document.getElementById("cfg-maxdepth").value),
    min_persist_bytes: Number(document.getElementById("cfg-minpersist-mb").value) * mib,
    scanner_workers: Number(document.getElementById("cfg-workers").value),
    auto_start: document.getElementById("cfg-autostart").checked,
    paused: document.getElementById("cfg-paused").checked,
    ignore_paths: document.getElementById("cfg-ignores").value.split(/\r?\n/).map((s) => s.trim()).filter(Boolean),
    alerts: {
      low_space_percent: Number(document.getElementById("cfg-low-pct").value),
      low_space_gb: Number(document.getElementById("cfg-low-gb").value),
      critical_space_percent: Number(document.getElementById("cfg-crit-pct").value),
      critical_space_gb: Number(document.getElementById("cfg-crit-gb").value),
      spike_bytes: Math.round(Number(document.getElementById("cfg-spike-gb").value) * gib),
      creeping_min_runs: Number(document.getElementById("cfg-creep-runs").value),
      creeping_min_bytes: Math.round(Number(document.getElementById("cfg-creep-gb").value) * gib),
      forecast_days_warn: Number(document.getElementById("cfg-forecast-days").value),
      cooldown_hours: Number(document.getElementById("cfg-cooldown").value),
      enable_toast: document.getElementById("cfg-toast").checked,
      webhook_url: document.getElementById("cfg-webhook").value.trim(),
      telegram_bot_token: document.getElementById("cfg-tg-token").value.trim(),
      telegram_chat_id: document.getElementById("cfg-tg-chat").value.trim(),
    },
  };

  const msgEl = document.getElementById("settings-msg");
  try {
    await api("/api/config", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(payload),
    });
    msgEl.textContent = "Đã lưu cài đặt!";
    await loadStatus();
  } catch (err) {
    msgEl.textContent = "Lỗi: " + err.message;
  }
});

document.addEventListener("click", (e) => {
  const el = e.target.closest("[data-action]");
  if (!el) return;
  const action = el.dataset.action;
  if (action === "scan-drive") window.triggerScan(el.dataset.drive || "");
  if (action === "diff-tag") window.setDiffTagFilter(el.dataset.tag || "");
  if (action === "open-explorer") window.openExplorer(el.dataset.path || "");
  if (action === "drill-explorer") window.drillExplorer(el.dataset.path || "");
  if (action === "ack-alert") window.ackAlert(Number(el.dataset.id));
});

// Poll status periodically so live scan progress updates smoothly.
loadStatus();
setInterval(() => {
  const active = state.status?.progress?.active;
  if (active || state.activeTab === "overview") {
    loadStatus().catch(() => {});
  }
}, 2500);

package shell

import (
	"encoding/json"
	"strings"
)

// Page renders the Settings window's initial HTML for a snapshot. The desktop
// adapter embeds it in the WebView; later snapshots are pushed through
// renderStatus. The page holds no routing, validation, or retry logic.
func Page(initial Snapshot) string {
	return strings.ReplaceAll(pageHTML, "__INITIAL__", jsonForScript(initial))
}

// jsonForScript encodes v as JSON that can sit inside an inline <script>.
// Angle brackets, ampersands, and the JavaScript line separators are escaped
// so an error message cannot close the element or break the statement.
func jsonForScript(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return strings.NewReplacer(
		"<", `\u003c`,
		">", `\u003e`,
		"&", `\u0026`,
		"\u2028", `\u2028`,
		"\u2029", `\u2029`,
	).Replace(string(data))
}

const pageHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>kitsune-whisper Settings</title>
<style>
  :root { color-scheme: light dark; --bg:#f6f6f7; --fg:#18181b; --muted:#6b6b74; --card:#ffffff; --line:#dedee3; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#18181b; --fg:#f4f4f5; --muted:#a1a1aa; --card:#232327; --line:#34343a; }
  }
  * { box-sizing: border-box; }
  body { margin:0; font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); }
  main { max-width: 640px; margin: 0 auto; padding: 40px 32px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  .sub { color: var(--muted); margin: 0 0 28px; }
  .card { background:var(--card); border:1px solid var(--line); border-radius:12px; padding:20px 22px; }
  .state { display:flex; align-items:center; gap:12px; }
  .dot { width:14px; height:14px; border-radius:50%; background:#8a8a8e; box-shadow:0 0 0 4px color-mix(in srgb, #8a8a8e 25%, transparent); }
  .state[data-state="recording"] .dot { background:#e5484d; box-shadow:0 0 0 4px color-mix(in srgb, #e5484d 25%, transparent); }
  .state[data-state="processing"] .dot { background:#f5a524; box-shadow:0 0 0 4px color-mix(in srgb, #f5a524 25%, transparent); }
  .state[data-state="error"] .dot { background:#d6409f; box-shadow:0 0 0 4px color-mix(in srgb, #d6409f 25%, transparent); }
  .state strong { font-size: 18px; }
  dl { display:grid; grid-template-columns: max-content 1fr; gap:8px 20px; margin:22px 0 0; }
  dt { color:var(--muted); }
  dd { margin:0; overflow-wrap:anywhere; }
  .error { margin-top:20px; padding:12px 14px; border:1px solid #d6409f55; border-radius:8px; color:#d6409f; display:none; }
  .error.visible { display:block; }
</style>
</head>
<body>
<main>
  <h1>kitsune-whisper</h1>
  <p class="sub">Private, LAN-local dictation</p>
  <section class="card">
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">Idle</strong></div>
    <dl>
      <dt>Phase</dt><dd id="phase">idle</dd>
      <dt>Client</dt><dd id="running">stopped</dd>
      <dt>Server profile</dt><dd id="server">—</dd>
      <dt>Version</dt><dd id="version">—</dd>
    </dl>
    <p class="error" id="error"></p>
  </section>
</main>
<script>
  function renderStatus(s) {
    const state = document.getElementById("state");
    state.dataset.state = s.state || "idle";
    document.getElementById("state-label").textContent = {
      idle: "Idle", recording: "Recording", processing: "Processing", error: "Error"
    }[s.state] || "Idle";
    document.getElementById("phase").textContent = s.phase || "idle";
    document.getElementById("running").textContent = s.running ? "running" : "stopped";
    document.getElementById("server").textContent = s.profile || "—";
    document.getElementById("version").textContent = s.version || "—";
    const error = document.getElementById("error");
    error.textContent = s.error || "";
    error.classList.toggle("visible", Boolean(s.error));
  }
  renderStatus(__INITIAL__);
</script>
</body>
</html>
`

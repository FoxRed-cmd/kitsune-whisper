package shell

import (
	"encoding/json"
	"html"
	"strings"

	"github.com/FoxRed-cmd/kitsune-whisper/client/internal/dictation"
)

// Page renders the Settings window's initial HTML for a status snapshot and a
// settings view. The desktop adapter serves it from the asset server; later
// snapshots arrive through renderStatus, renderSettings, renderMessages, and
// setLevel. The page holds no routing, validation, or retry logic: it collects
// values and sends one JSON intent, and Go decides.
func Page(initial Snapshot, settings dictation.View) string {
	title := settings.Title
	if title == "" {
		title = "kitsune-whisper"
	}
	subtitle := settings.Subtitle
	language := ""
	if settings.Language != "" {
		language = string(settings.Language)
	}
	return strings.NewReplacer(
		"__LANG__", html.EscapeString(language),
		"__TITLE__", html.EscapeString(title),
		"__SUBTITLE__", html.EscapeString(subtitle),
		"__SETTINGS__", SettingsHTML(settings),
		"__SAVE__", html.EscapeString(settings.Actions.Save),
		"__RESTART__", html.EscapeString(settings.Actions.Restart),
		"__RESTART_HINT__", html.EscapeString(settings.Actions.RestartHint),
		"__INITIAL__", jsonForScript(initial),
	).Replace(pageHTML)
}

// SettingsHTML renders the General and Dictation screens into an HTML fragment.
// Every string is escaped; the fragment is inserted into the page and also
// pushed verbatim after a save or a language switch.
func SettingsHTML(view dictation.View) string {
	var out strings.Builder
	for _, section := range view.Sections {
		out.WriteString(`<section class="group" data-section="` + escape(section.ID) + `">`)
		out.WriteString(`<h2>` + escape(section.Title) + `</h2>`)
		for _, field := range section.Fields {
			writeField(&out, field, view)
		}
		out.WriteString(`</section>`)
	}
	if view.DeviceError != "" {
		out.WriteString(`<p class="device-error">` + escape(view.DeviceError) + `</p>`)
	}
	return out.String()
}

func writeField(out *strings.Builder, field dictation.Field, view dictation.View) {
	id := "field-" + strings.NewReplacer(".", "-", "_", "-").Replace(field.Path)
	out.WriteString(`<div class="field" data-field="` + escape(field.Path) + `">`)
	out.WriteString(`<div class="field-head"><label for="` + escape(id) + `">` + escape(field.Label) + `</label>`)
	if field.Badge != "" {
		out.WriteString(`<span class="badge">` + escape(field.Badge) + `</span>`)
	}
	if field.Source != "default" && field.SourceLabel != "" {
		out.WriteString(`<span class="source">` + escape(field.SourceLabel) + `</span>`)
	}
	out.WriteString(`</div>`)

	disabled := ""
	if field.Locked {
		disabled = ` disabled`
	}
	switch field.Kind {
	case dictation.KindToggle:
		out.WriteString(`<input type="checkbox" id="` + escape(id) + `" data-path="` + escape(field.Path) + `"`)
		if field.Value == "true" {
			out.WriteString(` checked`)
		}
		out.WriteString(disabled + `>`)
	case dictation.KindSelect:
		writeSelect(out, id, field, disabled)
	case dictation.KindMicrophone:
		writeSelect(out, id, field, disabled)
		out.WriteString(`<div class="mic-actions">`)
		out.WriteString(`<button type="button" data-action="test-mic"` + disabled + `>` + escape(view.Actions.Test) + `</button>`)
		out.WriteString(`<button type="button" data-action="stop-mic"` + disabled + `>` + escape(view.Actions.Stop) + `</button>`)
		out.WriteString(`</div>`)
		out.WriteString(`<div class="meter" role="meter" aria-label="` + escape(view.Actions.Level) + `" aria-valuemin="0" aria-valuemax="100" aria-valuenow="0"><div class="meter-fill"></div></div>`)
	case dictation.KindHotkey:
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-path="` + escape(field.Path) +
			`" data-hotkey="true" autocomplete="off" spellcheck="false" value="` + escape(field.Value) +
			`" placeholder="` + escape(view.Actions.HotkeyPlaceholder) + `"` + disabled + `>`)
	default:
		out.WriteString(`<input type="text" id="` + escape(id) + `" data-path="` + escape(field.Path) +
			`" autocomplete="off" spellcheck="false" value="` + escape(field.Value) + `"` + disabled + `>`)
	}
	if field.Help != "" {
		out.WriteString(`<p class="help">` + escape(field.Help) + `</p>`)
	}
	if field.Locked {
		out.WriteString(`<p class="lock">` + escape(field.LockReason) + `</p>`)
	}
	out.WriteString(`</div>`)
}

func writeSelect(out *strings.Builder, id string, field dictation.Field, disabled string) {
	out.WriteString(`<select id="` + escape(id) + `" data-path="` + escape(field.Path) + `"` + disabled + `>`)
	for _, option := range field.Options {
		out.WriteString(`<option value="` + escape(option.Value) + `"`)
		if option.Value == field.Value {
			out.WriteString(` selected`)
		}
		out.WriteString(`>` + escape(option.Label) + `</option>`)
	}
	out.WriteString(`</select>`)
}

func escape(value string) string { return html.EscapeString(value) }

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
<html lang="__LANG__">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>__TITLE__</title>
<style>
  :root { color-scheme: light dark; --bg:#f6f6f7; --fg:#18181b; --muted:#6b6b74; --card:#ffffff; --line:#dedee3; --accent:#4c6ef5; }
  @media (prefers-color-scheme: dark) {
    :root { --bg:#18181b; --fg:#f4f4f5; --muted:#a1a1aa; --card:#232327; --line:#34343a; --accent:#748ffc; }
  }
  * { box-sizing: border-box; }
  body { margin:0; font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; background:var(--bg); color:var(--fg); }
  main { max-width: 720px; margin: 0 auto; padding: 36px 32px 64px; }
  h1 { font-size: 22px; margin: 0 0 4px; }
  .sub { color: var(--muted); margin: 0 0 24px; }
  .card, .group { background:var(--card); border:1px solid var(--line); border-radius:12px; padding:20px 22px; margin-bottom:18px; }
  .group h2 { font-size: 16px; margin: 0 0 14px; }
  .state { display:flex; align-items:center; gap:12px; }
  .dot { width:14px; height:14px; border-radius:50%; background:#8a8a8e; box-shadow:0 0 0 4px color-mix(in srgb, #8a8a8e 25%, transparent); }
  .state[data-state="recording"] .dot { background:#e5484d; box-shadow:0 0 0 4px color-mix(in srgb, #e5484d 25%, transparent); }
  .state[data-state="processing"] .dot { background:#f5a524; box-shadow:0 0 0 4px color-mix(in srgb, #f5a524 25%, transparent); }
  .state[data-state="error"] .dot { background:#d6409f; box-shadow:0 0 0 4px color-mix(in srgb, #d6409f 25%, transparent); }
  .state strong { font-size: 18px; }
  dl { display:grid; grid-template-columns: max-content 1fr; gap:8px 20px; margin:18px 0 0; }
  dt { color:var(--muted); }
  dd { margin:0; overflow-wrap:anywhere; }
  .field { padding: 12px 0; border-top: 1px solid var(--line); }
  .field:first-of-type { border-top: none; }
  .field-head { display:flex; align-items:center; gap:8px; flex-wrap:wrap; margin-bottom:6px; }
  .field-head label { font-weight: 600; }
  .field input[type="text"], .field select { width:100%; padding:8px 10px; border:1px solid var(--line); border-radius:8px; background:var(--bg); color:var(--fg); font: inherit; }
  .field input[type="checkbox"] { width:18px; height:18px; }
  .field input[disabled], .field select[disabled], .field button[disabled] { opacity:.6; cursor:not-allowed; }
  .help { color:var(--muted); margin:6px 0 0; font-size: 13px; }
  .lock { color:#c2410c; margin:6px 0 0; font-size: 13px; }
  .badge { font-size: 12px; padding:2px 8px; border-radius:999px; background:#f5a52433; color:#b45309; }
  .source { font-size: 12px; color:var(--muted); }
  .mic-actions { display:flex; gap:8px; margin-top:8px; }
  .meter { height:10px; border-radius:999px; background:var(--line); overflow:hidden; margin-top:8px; }
  .meter-fill { height:100%; width:0; background:var(--accent); transition:width 60ms linear; }
  .device-error { color:#d6409f; }
  .messages { margin-bottom:18px; }
  .notice { color:#2f9e44; margin:0 0 6px; }
  .failure { color:#d6409f; margin:0 0 6px; }
  .error { margin-top:20px; padding:12px 14px; border:1px solid #d6409f55; border-radius:8px; color:#d6409f; display:none; }
  .error.visible { display:block; }
  footer { display:flex; gap:10px; align-items:center; margin-top: 20px; }
  button { font: inherit; padding: 8px 14px; border-radius: 8px; border:1px solid var(--line); background: var(--card); color: var(--fg); cursor: pointer; }
  button.primary { background: var(--accent); border-color: var(--accent); color: white; }
  .hint { color: var(--muted); font-size: 13px; margin-left: auto; }
</style>
</head>
<body>
<main>
  <h1>__TITLE__</h1>
  <p class="sub">__SUBTITLE__</p>
  <section class="card">
    <div class="state" id="state" data-state="idle"><span class="dot"></span><strong id="state-label">—</strong></div>
    <dl>
      <dt id="label-phase">—</dt><dd id="phase">idle</dd>
      <dt id="label-running">—</dt><dd id="running">—</dd>
      <dt id="label-server">—</dt><dd id="server">—</dd>
      <dt id="label-version">—</dt><dd id="version">—</dd>
    </dl>
    <p class="error" id="error"></p>
  </section>
  <div class="messages" id="messages"></div>
  <div id="settings">__SETTINGS__</div>
  <footer>
    <button type="button" class="primary" data-action="save" id="save-button">__SAVE__</button>
    <button type="button" data-action="restart" id="restart-button">__RESTART__</button>
    <span class="hint" id="restart-hint">__RESTART_HINT__</span>
  </footer>
</main>
<script>
  function text(id, value) {
    var el = document.getElementById(id);
    if (el) el.textContent = value || "";
  }
  function renderStatus(s) {
    var labels = s.labels || {};
    var state = document.getElementById("state");
    if (state) state.dataset.state = s.state || "idle";
    text("state-label", labels.state);
    text("phase", labels.phaseValue);
    text("running", s.running ? labels.running : labels.stopped);
    text("server", s.profile || "—");
    text("version", s.version || "—");
    text("label-phase", labels.phase);
    text("label-running", labels.client);
    text("label-server", labels.profile);
    text("label-version", labels.version);
    var error = document.getElementById("error");
    if (error) {
      error.textContent = s.error || "";
      error.classList.toggle("visible", Boolean(s.error));
    }
  }
  function renderSettings(v) {
    var host = document.getElementById("settings");
    if (host) host.innerHTML = (v && v.html) || "";
    if (v && v.language) document.documentElement.lang = v.language;
    if (v && v.actions) {
      var save = document.getElementById("save-button");
      if (save && v.actions.save) save.textContent = v.actions.save;
      var restart = document.getElementById("restart-button");
      if (restart && v.actions.restart) restart.textContent = v.actions.restart;
      var hint = document.getElementById("restart-hint");
      if (hint) hint.textContent = v.actions.restartHint || "";
    }
  }
  function renderMessages(m) {
    var host = document.getElementById("messages");
    if (!host) return;
    host.textContent = "";
    ((m && m.notices) || []).forEach(function (line) {
      var p = document.createElement("p");
      p.className = "notice";
      p.textContent = line;
      host.appendChild(p);
    });
    ((m && m.failures) || []).forEach(function (line) {
      var p = document.createElement("p");
      p.className = "failure";
      p.textContent = line;
      host.appendChild(p);
    });
  }
  function setLevel(level) {
    var fill = document.querySelector("#settings .meter-fill");
    var meter = document.querySelector("#settings .meter[role='meter']");
    var clamped = Math.max(0, Math.min(1, Number(level) || 0));
    if (fill) fill.style.width = (clamped * 100).toFixed(1) + "%";
    if (meter) meter.setAttribute("aria-valuenow", String(Math.round(clamped * 100)));
  }
  function collectPatch() {
    var patch = {};
    var fields = document.querySelectorAll("#settings [data-path]");
    for (var i = 0; i < fields.length; i++) {
      var el = fields[i];
      if (el.disabled) continue;
      var path = el.dataset.path.replace(/^client\./, "");
      var parts = path.split(".");
      var cursor = patch;
      for (var j = 0; j < parts.length - 1; j++) {
        if (!cursor[parts[j]]) cursor[parts[j]] = {};
        cursor = cursor[parts[j]];
      }
      cursor[parts[parts.length - 1]] = el.type === "checkbox" ? el.checked : el.value;
    }
    return patch;
  }
  function sendIntent(intent) {
    fetch("/kitsune/intent", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(intent)
    }).catch(function () {});
  }
  function hotkeySpec(event) {
    var key = event.key;
    var name = null;
    if (key === " ") name = "Space";
    else if (key === "Escape") name = "Esc";
    else if (key === "Enter") name = "Enter";
    else if (key === "Delete") name = "Delete";
    else if (key === "ArrowLeft") name = "Left";
    else if (key === "ArrowRight") name = "Right";
    else if (key === "ArrowUp") name = "Up";
    else if (key === "ArrowDown") name = "Down";
    else if (key === "Tab") name = "Tab";
    else if (/^[a-zA-Z]$/.test(key)) name = key.toUpperCase();
    else if (/^[0-9]$/.test(key)) name = key;
    else if (/^F([1-9]|1[0-9]|20)$/.test(key)) name = key;
    if (!name) return null;
    var parts = [];
    if (event.ctrlKey) parts.push("Ctrl");
    if (event.altKey) parts.push("Alt");
    if (event.shiftKey) parts.push("Shift");
    if (event.metaKey) parts.push("Super");
    parts.push(name);
    return parts.join("+");
  }
  document.addEventListener("click", function (event) {
    var button = event.target.closest ? event.target.closest("[data-action]") : null;
    if (!button) return;
    var action = button.dataset.action;
    if (action === "save") sendIntent({ action: "save", patch: collectPatch() });
    else if (action === "restart") sendIntent({ action: "restart" });
    else if (action === "test-mic") {
      var input = document.querySelector("#settings [data-path='client.audio.device']");
      sendIntent({ action: "test-mic", device: input ? input.value : "" });
    } else if (action === "stop-mic") {
      setLevel(0);
      sendIntent({ action: "stop-mic" });
    }
  });
  document.addEventListener("change", function (event) {
    var el = event.target.closest ? event.target.closest("[data-path]") : null;
    if (!el) return;
    if (el.dataset.path === "client.ui_language") {
      sendIntent({ action: "save", patch: { ui_language: el.value } });
    }
  });
  document.addEventListener("keydown", function (event) {
    var el = event.target.closest ? event.target.closest("[data-hotkey]") : null;
    if (!el) return;
    if (event.key === "Tab") return;
    event.preventDefault();
    var spec = hotkeySpec(event);
    if (!spec) return;
    el.value = spec;
    sendIntent({ action: "validate", path: el.dataset.path, value: spec });
  });
  renderStatus(__INITIAL__);
</script>
</body>
</html>
`

// The pairing page: one button, one QR code.
//
// Loopback only (see loopbackOnly) — this page hands out the secret, so
// it is for the person sitting at the machine and nobody else. It is
// plain server-rendered HTML with no build step, because a front end
// that needs npm to show a QR code is a front end that will rot before
// the feature it introduces.
package main

import (
	"encoding/json"
	"html/template"
	"net/http"

	qrcode "github.com/skip2/go-qrcode"
)

func (l *lanTransport) pairJSON(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, l.id.pairing(l.Hints()))
}

func (l *lanTransport) pairPNG(w http.ResponseWriter, r *http.Request) {
	payload, err := json.Marshal(l.id.pairing(l.Hints()))
	if err != nil {
		http.Error(w, "cannot encode pairing", http.StatusInternalServerError)
		return
	}
	// Medium recovery: this code is read off a bright screen from six
	// inches away, not off a shipping label. Spending redundancy here
	// only makes the modules smaller and the scan harder.
	png, err := qrcode.Encode(string(payload), qrcode.Medium, 512)
	if err != nil {
		http.Error(w, "cannot draw pairing code", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

func (l *lanTransport) page(w http.ResponseWriter, r *http.Request) {
	hints := l.Hints()
	data := struct {
		Name    string
		Hints   []string
		Unreach bool
	}{l.id.Name, hints, len(hints) == 0}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = pairPage.Execute(w, data)
}

var pairPage = template.Must(template.New("pair").Parse(`<!doctype html>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Pair with {{.Name}}</title>
<style>
  :root { color-scheme: dark }
  body { margin:0; min-height:100vh; display:grid; place-items:center;
         background:#161826; color:#E9E9ED;
         font:15px/1.6 ui-sans-serif,-apple-system,"Inter",system-ui,sans-serif }
  main { width:min(420px,90vw); text-align:center }
  h1 { font-size:20px; font-weight:600; margin:0 0 6px }
  p { color:#8b8d9b; margin:0 0 28px; font-size:13px }
  button { font:inherit; font-size:14px; color:#D2CEFD; background:transparent;
           border:1px solid #9184D9; border-radius:10px; padding:11px 22px; cursor:pointer }
  button:hover { background:#232532 }
  figure { margin:0; display:none }
  figure.on { display:block }
  img { width:280px; height:280px; background:#fff; border-radius:12px; padding:14px }
  code { display:block; color:#8b8d9b; font-size:11.5px; margin-top:14px }
  .warn { color:#D2CEFD; font-size:12.5px; margin-top:20px }
</style>
<main>
  <h1>{{.Name}}</h1>
  <p>Scan this with Vera on your phone. It pairs once and stays paired.</p>

  <button id="go">Show pairing code</button>

  <figure id="code">
    <!-- src is set on click, not on load: the page should not put the
         secret on screen just because a browser tab was left open. -->
    <img id="qr" alt="Pairing code">
    {{range .Hints}}<code>{{.}}</code>{{end}}
  </figure>

  {{if .Unreach}}<p class="warn">No LAN address on this machine — nothing
  could reach it right now. Check wifi.</p>{{end}}
</main>
<script>
  const b = document.getElementById('go');
  b.onclick = () => {
    document.getElementById('qr').src = '/pair.png?t=' + Date.now();
    document.getElementById('code').classList.add('on');
    b.remove();
  };
</script>
`))

// The whiteboard theme's contrast, measured in the page it runs in
// (whiteboard_theme_windows_test.go evaluates it in WebView2). It waits for
// the board to render, then posts "contrast:" and a JSON report: every text
// against what's behind it (WCAG 2.2 1.4.3: 4.5:1, or 3:1 for large text),
// the edges of text fields, checkboxes and radio buttons (1.4.11: 3:1) and
// every pen stroke against the canvas (3:1).
(async () => {
  const post = (m) => window.chrome.webview.postMessage(m);
  try {
    for (let i = 0; i < 100 && !document.querySelector('[data-slot="board-block"]'); i++) await new Promise((r) => setTimeout(r, 100));
    await new Promise((r) => setTimeout(r, 800));

    // Any CSS colour (oklch, color-mix, ...) as sRGB 0..255 and alpha 0..1, through a canvas.
    const ctx = document.createElement("canvas").getContext("2d", { willReadFrequently: true });
    const rgba = (css) => {
      ctx.clearRect(0, 0, 1, 1);
      ctx.fillStyle = "rgba(0,0,0,0)";
      ctx.fillStyle = css;
      ctx.fillRect(0, 0, 1, 1);
      const d = ctx.getImageData(0, 0, 1, 1).data;
      return [d[0], d[1], d[2], d[3] / 255];
    };
    const over = (top, under) => {
      const a = top[3];
      return [top[0] * a + under[0] * (1 - a), top[1] * a + under[1] * (1 - a), top[2] * a + under[2] * (1 - a), 1];
    };
    const lum = ([r, g, b]) => {
      const f = (c) => ((c /= 255) <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4);
      return 0.2126 * f(r) + 0.7152 * f(g) + 0.0722 * f(b);
    };
    const ratio = (a, b) => {
      const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p);
      return (x + 0.05) / (y + 0.05);
    };
    const hex = (c) => "#" + c.slice(0, 3).map((v) => Math.round(v).toString(16).padStart(2, "0")).join("");

    const page = rgba(getComputedStyle(document.body).backgroundColor);
    const pageColour = page[3] > 0 ? over(page, [255, 255, 255, 1]) : rgba(getComputedStyle(document.documentElement).backgroundColor);
    // What's behind el: its own and its ancestors' background colours, laid over each other.
    const behind = (el) => {
      const layers = [];
      for (let e = el; e && e !== document.documentElement; e = e.parentElement) {
        const c = rgba(getComputedStyle(e).backgroundColor);
        if (c[3] > 0) layers.push(c);
        if (c[3] >= 1) break;
      }
      let bg = layers.length && layers[layers.length - 1][3] >= 1 ? layers.pop() : pageColour;
      while (layers.length) bg = over(layers.pop(), bg);
      return bg;
    };
    const shown = (el) => {
      if (!el.getClientRects().length) return false;
      for (let e = el; e; e = e.parentElement) {
        const s = getComputedStyle(e);
        if (s.visibility === "hidden" || s.display === "none" || +s.opacity === 0) return false;
      }
      return true;
    };
    const describe = (el, text) => {
      const slot = el.closest("[data-slot]")?.getAttribute("data-slot") ?? el.tagName.toLowerCase();
      return `${slot}: ${text.trim().slice(0, 40)}`;
    };

    const failures = [];
    let checked = 0;
    let minText = 99;
    let minEdge = 99;
    let minInk = 99;
    const check = (kind, el, what, fg, bg, need) => {
      const r = ratio(fg, bg);
      checked++;
      if (kind === "text") minText = Math.min(minText, r);
      if (kind === "control edge") minEdge = Math.min(minEdge, r);
      if (kind === "pen stroke") minInk = Math.min(minInk, r);
      if (r < need) failures.push({ kind, what, ratio: +r.toFixed(2), need, fg: hex(fg), bg: hex(bg) });
    };

    // Text: elements with their own text, and the values in text fields.
    for (const el of document.querySelectorAll("body *")) {
      if (el.closest("svg, script, style, [aria-hidden='true'], #print")) continue;
      const own = Array.from(el.childNodes).some((n) => n.nodeType === 3 && n.textContent.trim());
      const value = (el.tagName === "INPUT" || el.tagName === "TEXTAREA") && el.value?.trim() ? el.value : "";
      if (!own && !value) continue;
      if (!shown(el)) continue;
      const s = getComputedStyle(el);
      const bg = behind(el);
      const fg = over(rgba(s.color), bg);
      const size = parseFloat(s.fontSize);
      const large = size >= 24 || (size >= 18.66 && +s.fontWeight >= 700);
      check("text", el, describe(el, value || el.textContent), fg, bg, large ? 3 : 4.5);
    }

    // Control edges: text fields, checkboxes, radio buttons, select triggers.
    for (const el of document.querySelectorAll('[data-slot="input"], [data-slot="checkbox"], [data-slot="radio"], [data-slot="select-trigger"]')) {
      if (!shown(el)) continue;
      const s = getComputedStyle(el);
      if (parseFloat(s.borderTopWidth) === 0) continue;
      const edge = rgba(s.borderTopColor);
      if (edge[3] === 0) continue; // a borderless field (a frame's name, a card's title): its text is checked
      const bg = behind(el.parentElement);
      // A checked box is filled: its fill is its edge.
      const fill = rgba(s.backgroundColor);
      const fg = over(fill[3] >= 1 && el.hasAttribute("data-checked") ? fill : edge, bg);
      check("control edge", el, describe(el, el.getAttribute("aria-label") ?? el.getAttribute("data-slot")), fg, bg, 3);
    }

    // Pen strokes against the canvas.
    const canvas = rgba(getComputedStyle(document.querySelector('[data-slot="board"]')).backgroundColor);
    for (const el of document.querySelectorAll('[data-slot="board-ink-line"]')) {
      const s = getComputedStyle(el);
      const c = rgba(el.hasAttribute("data-filled") ? s.fill : s.stroke);
      check("pen stroke", el, el.closest("[data-block-id]")?.getAttribute("data-block-id") ?? "stroke", over(c, canvas), canvas, 3);
    }

    post(
      "contrast:" +
        JSON.stringify({
          theme: document.documentElement.dataset.theme ?? "",
          checked,
          minText: +minText.toFixed(2),
          minEdge: +minEdge.toFixed(2),
          minInk: +minInk.toFixed(2),
          failures,
        }),
    );
  } catch (e) {
    post("contrast:" + JSON.stringify({ theme: "", checked: 0, failures: [{ kind: "error", what: String(e && e.stack || e), ratio: 0, need: 0, fg: "", bg: "" }] }));
  }
})();

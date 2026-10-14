function layout(scroll, hit) {
  const e = this;
  if (!(e instanceof Element) || !e.isConnected || e.ownerDocument !== document) {
    return { connected: false };
  }
  if (scroll) e.scrollIntoView({ block: "center", inline: "center", behavior: "instant" });

  const r = e.getBoundingClientRect();
  const s = getComputedStyle(e);
  let left = Math.max(0, r.left), right = Math.min(innerWidth, r.right);
  let top = Math.max(0, r.top), bottom = Math.min(innerHeight, r.bottom);
  let visible = r.width > 0 && r.height > 0 && s.visibility === "visible" &&
    s.display !== "none" && e.getClientRects().length > 0;
  let disabled = e.matches(":disabled") || e.getAttribute("aria-disabled") === "true";
  for (let p = e; p; p = p.assignedSlot || p.parentElement || p.getRootNode().host) {
    const ps = getComputedStyle(p);
    if (ps.opacity === "0" || ps.display === "none" || p.hidden) visible = false;
    if (p.inert || p.getAttribute("aria-disabled") === "true") disabled = true;
    if (p !== e) {
      const b = p.getBoundingClientRect();
      if (/auto|scroll|hidden|clip/.test(ps.overflowX)) {
        left = Math.max(left, b.left);
        right = Math.min(right, b.right);
      }
      if (/auto|scroll|hidden|clip/.test(ps.overflowY)) {
        top = Math.max(top, b.top);
        bottom = Math.min(bottom, b.bottom);
      }
    }
  }

  const viewport = right > left && bottom > top;
  let x = (left + right) / 2, y = (top + bottom) / 2, obscured = hit;
  const owns = n => {
    while (n) {
      if (n === e || e.contains(n)) return true;
      n = n.assignedSlot || n.parentNode || n.host;
    }
    return false;
  };
  if (hit && viewport) {
    for (const [a, b] of [[.5, .5], [.2, .2], [.8, .2], [.2, .8], [.8, .8]]) {
      const px = left + (right - left) * a, py = top + (bottom - top) * b;
      let n = document.elementFromPoint(px, py);
      while (n && n.shadowRoot) {
        const child = n.shadowRoot.elementFromPoint(px, py);
        if (!child || child === n) break;
        n = child;
      }
      if (owns(n)) { x = px; y = py; obscured = false; break; }
    }
  }

  const boolAttr = n => e.hasAttribute(n) ? e.getAttribute(n) === "true" : undefined;
  const tri = n => e.hasAttribute(n) ?
    (e.getAttribute(n) === "mixed" ? "mixed" : e.getAttribute(n) === "true") : undefined;
  let active = document.activeElement;
  while (active?.shadowRoot?.activeElement) active = active.shadowRoot.activeElement;
  const focused = active === e || e.getRootNode().activeElement === e;
  const sensitive = ["password", "file", "hidden"].includes(e.type) ||
    /(cc-|one-time-code)/i.test(e.autocomplete || "");
  const raw = !sensitive && ("value" in e) &&
    !["checkbox", "radio", "submit", "button", "reset"].includes(e.type) ? String(e.value) : undefined;
  const state = {
    checked: ("checked" in e) ? (e.indeterminate ? "mixed" : e.checked) : tri("aria-checked"),
    selected: ("selected" in e) ? e.selected : boolAttr("aria-selected"),
    expanded: boolAttr("aria-expanded"), pressed: tri("aria-pressed"), focused,
    readonly: !!e.readOnly || e.getAttribute("aria-readonly") === "true",
    required: !!e.required || e.getAttribute("aria-required") === "true",
    value: raw?.slice(0, 512), valueTruncated: raw !== undefined && raw.length > 512,
    placeholder: e.getAttribute("placeholder") || e.getAttribute("aria-placeholder") || undefined,
    inputType: e instanceof HTMLInputElement ? e.type : undefined,
    scrollable: (e.scrollHeight > e.clientHeight && /auto|scroll/.test(s.overflowY)) ||
      (e.scrollWidth > e.clientWidth && /auto|scroll/.test(s.overflowX)),
    scrollAbove: e.scrollTop,
    scrollBelow: Math.max(0, e.scrollHeight - e.clientHeight - e.scrollTop)
  };
  if (e instanceof HTMLSelectElement && !sensitive) {
    state.options = Array.from(e.options).slice(0, 5).map(o => ({
      index: o.index, value: o.value.slice(0, 128), label: o.label.slice(0, 128),
      disabled: o.disabled || !!o.parentElement?.disabled, selected: o.selected,
      truncated: o.value.length > 128 || o.label.length > 128
    }));
    state.optionCount = e.options.length;
    state.optionsTruncated = e.options.length > 5;
  }
  if (!state.scrollable) { delete state.scrollAbove; delete state.scrollBelow; }
  const editable = !state.readonly && !disabled &&
    ((e instanceof HTMLInputElement && ["text", "email", "password", "search", "tel", "url"].includes(e.type)) ||
      e instanceof HTMLTextAreaElement || e.isContentEditable);
  return { connected: true, visible, viewport, disabled, editable, x, y, left, right, top, bottom, obscured, state };
}

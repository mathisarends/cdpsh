function options(offset, limit) {
  if (!(this instanceof HTMLSelectElement)) return { unsupported: true };
  const sensitive = /(cc-|one-time-code)/i.test(this.autocomplete || "");
  return {
    total: this.options.length,
    options: Array.from(this.options).slice(offset, offset + limit).map(o => ({
      index: o.index, value: sensitive ? "" : o.value.slice(0, 128), valueRedacted: sensitive,
      label: o.label.slice(0, 128), disabled: o.disabled || !!o.parentElement?.disabled,
      selected: o.selected, truncated: (!sensitive && o.value.length > 128) || o.label.length > 128
    }))
  };
}

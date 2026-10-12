function selectState(value, label, index) {
  if (!this.isConnected) return { error: "STALE_REF" };
  if (!(this instanceof HTMLSelectElement)) return { error: "UNSUPPORTED" };
  if (this.multiple) return { error: "UNSUPPORTED" };
  const options = Array.from(this.options).filter(o =>
    index !== null ? o.index === Number(index) : (value !== null ? o.value === value : o.label === label));
  if (options.length !== 1) return { error: options.length ? "AMBIGUOUS_TARGET" : "TARGET_NOT_FOUND" };
  const o = options[0];
  if (o.disabled || o.parentElement?.disabled) return { error: "DISABLED" };
  return { index: o.index, same: this.selectedIndex === o.index, value: o.value, label: o.label };
}

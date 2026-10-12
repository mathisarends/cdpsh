function select(index, value, label) {
  const o = this.options[index];
  if (!this.isConnected || !o || o.disabled || o.parentElement?.disabled || o.value !== value || o.label !== label) {
    return false;
  }
  this.selectedIndex = index;
  this.dispatchEvent(new Event("input", { bubbles: true, composed: true }));
  this.dispatchEvent(new Event("change", { bubbles: true }));
  return this.isConnected && this.selectedIndex === index &&
    this.options[index]?.value === value && this.options[index]?.label === label;
}

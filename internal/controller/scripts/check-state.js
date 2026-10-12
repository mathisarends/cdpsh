function checkState() {
  const role = this.getAttribute("role");
  const native = this instanceof HTMLInputElement && ["checkbox", "radio"].includes(this.type);
  return {
    connected: this.isConnected,
    supported: native || ["checkbox", "switch", "radio"].includes(role),
    radio: this.type === "radio" || role === "radio",
    checked: native ? this.checked : this.getAttribute("aria-checked") === "true",
    mixed: this.indeterminate || this.getAttribute("aria-checked") === "mixed"
  };
}

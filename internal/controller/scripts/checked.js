function checked(want) {
  return this.isConnected && !(this.indeterminate || this.getAttribute("aria-checked") === "mixed") &&
    (("checked" in this) ? this.checked : this.getAttribute("aria-checked") === "true") === want;
}

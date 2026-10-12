function focus() {
  this.focus();
  return this.getRootNode().activeElement === this;
}

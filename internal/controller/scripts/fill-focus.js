function fillFocus() {
  this.focus();
  if (this.isContentEditable) {
    const range = document.createRange();
    range.selectNodeContents(this);
    const selection = document.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
  } else {
    this.select();
  }
  return true;
}

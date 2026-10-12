package controller

import _ "embed"

// Keep browser-side logic as readable JavaScript while retaining a standalone
// binary. Each function is executed against an explicitly resolved DOM object.

//go:embed scripts/layout.js
var layoutJS string

//go:embed scripts/focus.js
var focusJS string

//go:embed scripts/check-state.js
var checkStateJS string

//go:embed scripts/checked.js
var checkedJS string

//go:embed scripts/select-state.js
var selectStateJS string

//go:embed scripts/select.js
var selectJS string

//go:embed scripts/options.js
var optionsJS string

//go:embed scripts/fill-focus.js
var fillFocusJS string

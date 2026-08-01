package artifacts

import _ "embed"

// blankPresentationTemplate contains a standards-compliant PowerPoint package.
// Slides are added at runtime, so creating PPTX artifacts needs no Office install.
//
//go:embed templates/blank.pptx
var blankPresentationTemplate []byte

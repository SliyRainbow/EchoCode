//go:build !windows

package browserengine

type unsupportedNativeBrowserSurface struct{}

func newNativeBrowserSurface() nativeBrowserSurface { return unsupportedNativeBrowserSurface{} }

func (unsupportedNativeBrowserSurface) Supported() bool  { return false }
func (unsupportedNativeBrowserSurface) Attach(int) error { return nil }
func (unsupportedNativeBrowserSurface) Show(NativeSurfaceBounds, nativeWindowInsets) error {
	return nil
}
func (unsupportedNativeBrowserSurface) Hide() error { return nil }
func (unsupportedNativeBrowserSurface) Close()      {}

//go:build windows

package localipc

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"github.com/Microsoft/go-winio"
	"github.com/hunknownz/Meerkat/internal/platform"
	"golang.org/x/sys/windows"
	"net"
	"strings"
)

func Endpoint(path string) string {
	sid, e := platform.UserSID()
	if e != nil {
		return ""
	}
	digest := sha256.Sum256([]byte(sid.String() + "\x00" + strings.ToLower(path)))
	return fmt.Sprintf(`\\.\pipe\meerkat-%x`, digest[:20])
}
func Check(path string) error {
	if !strings.HasPrefix(path, `\\.\pipe\meerkat-`) {
		return errors.New("invalid private pipe")
	}
	return nil
}
func Dial(ctx context.Context, path string) (net.Conn, error) {
	if e := Check(path); e != nil {
		return nil, e
	}
	c, e := winio.DialPipeContext(ctx, path)
	if e != nil {
		return nil, e
	}
	fd, ok := c.(interface{ Fd() uintptr })
	if !ok {
		c.Close()
		return nil, errors.New("pipe ownership unavailable")
	}
	sd, e := windows.GetSecurityInfo(windows.Handle(fd.Fd()), windows.SE_KERNEL_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if e != nil || !platform.PrivateSD(sd) {
		c.Close()
		return nil, errors.New("unsafe pipe owner or permissions")
	}
	return c, nil
}
func Listen(path string) (net.Listener, error) {
	if e := Check(path); e != nil {
		return nil, e
	}
	sid, e := platform.UserSID()
	if e != nil {
		return nil, e
	}
	return winio.ListenPipe(path, &winio.PipeConfig{SecurityDescriptor: "O:" + sid.String() + "D:P(A;;GA;;;" + sid.String() + ")(A;;GA;;;SY)", MessageMode: true, InputBufferSize: 65536, OutputBufferSize: 65536})
}

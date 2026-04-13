package platform

import "errors"

var ErrUnsupportedPlatform = errors.New("toolbox daemon is not supported on windows")

const DaemonExecutableEnv = "TOOLBOX_DAEMON_EXE"

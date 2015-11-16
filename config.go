package apns

import "os"
import "github.com/op/go-logging"

var log = logging.MustGetLogger("apns-go")

var format = logging.MustStringFormatter(
	"%{color:bold}%{time:15:04:05.000} %{shortpkg}.%{longfunc} pid:%{pid} ▶ %{level:.4s} %{color:reset} %{message}",
)

func setLogger() {
	backendError := logging.NewLogBackend(os.Stderr, "", 0)
	backendRegular := logging.NewLogBackend(os.Stdout, "", 0)
	backendErrorLeveled := logging.AddModuleLevel(backendError)
	backendErrorLeveled.SetLevel(logging.WARNING, "")
	backendErrorFormatter := logging.NewBackendFormatter(backendErrorLeveled, format)
	backendRegularFormatter := logging.NewBackendFormatter(backendRegular, format)
	logging.SetBackend(backendErrorFormatter, backendRegularFormatter)
}

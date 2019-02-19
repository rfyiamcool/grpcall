package grpcall

import (
	"strings"
)

func parseSymbol(svcAndMethod string) (string, string) {
	pos := strings.LastIndex(svcAndMethod, "/")
	if pos < 0 {
		pos = strings.LastIndex(svcAndMethod, ".")
		if pos < 0 {
			return "", ""
		}
	}

	return svcAndMethod[:pos], svcAndMethod[pos+1:]
}

// null logger
var defualtLogger = func(level, s string) {}

type loggerType func(level, s string)

func SetLogger(logger loggerType) {
	defualtLogger = logger
}

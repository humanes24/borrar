package system

import (
	"strings"

	"github.com/shirou/gopsutil/host"
)

var hostId string

func init() {
	hostId, _ = host.HostID()
}
func GetUniqueID() string {
	return strings.ToUpper(strings.ReplaceAll(hostId, "-", ""))
}

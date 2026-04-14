package system

import (
	"log"
	"strings"

	"github.com/shirou/gopsutil/host"
)

var hostId string

func init() {
	var err error
	hostId, err = host.HostID()
	if err != nil {
		log.Printf("WARNING: unable to get host ID: %v", err)
	}
}
func GetUniqueID() string {
	return strings.ToUpper(strings.ReplaceAll(hostId, "-", ""))
}

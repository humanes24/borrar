package iface_guard

import (
	_ "embed"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"
	"time"

	"github.com/influxdata/telegraf"
	system_utils "github.com/influxdata/telegraf/plugins/common/system"
	"github.com/influxdata/telegraf/plugins/inputs"
)

//go:embed sample.conf
var sampleConfig string

type netInterface struct {
	Timestamp  int64  `json:"timestamp"`  //ts del ultimo evento
	State      string `json:"state"`      //ultimo estado
	Interface  string `json:"interface"`  //nombre de la interfaz
	IfaceType  string `json:"ifaceType"`  //wifi/ethernet ...
	MACAddress string `json:"macAddress"` //macadd
}
type IfacesGuard struct {
	IfacesTracked []string                 `toml:"ifaces_tracked"`
	netInterfaces map[string]*netInterface `toml:"-"`
	Log           telegraf.Logger          `toml:"-"`
}

// Define el nombre del plugin
func (ig *IfacesGuard) SampleConfig() string {
	return sampleConfig
}
func (ig *IfacesGuard) Init() error {
	ig.Log.Info("ifaces guard collect started")
	ig.netInterfaces = make(map[string]*netInterface)
	return nil
}
func (ig *IfacesGuard) Gather(acc telegraf.Accumulator) error {
	var err error
	var ifaces map[string]*netInterface
	var sysMetrics []system_utils.SystemMetric

	ifaces, err = ig.getWhiteListInterfaces()
	if err != nil {
		ig.Log.Info("error getting whitelist interface")
		return err
	}
	for ifaceName, iface := range ifaces {
		onMemoryIface, found := ig.netInterfaces[ifaceName]
		if !found {
			ig.Log.Infof("new iface created. Name: %v Mac: %v Type: %v\n", ifaceName, iface.MACAddress, iface.IfaceType)
			ig.netInterfaces[ifaceName] = iface
		} else if onMemoryIface.State != iface.State {
			ig.Log.Infof("iface %v - %v changed its connection state: before: %v - now: %v\n", ifaceName, iface.MACAddress, onMemoryIface.State, iface.State)
			onMemoryIface.State = iface.State
			sysMetrics = append(sysMetrics, iface)
		}
	}
	if len(sysMetrics) != 0 {
		for _, sysMetric := range sysMetrics {
			me := sysMetric.TelegrafNormalize()
			acc.AddFields(me.GetDeviceID(), me.GetFields(), me.GetTags(), me.GetTime())
		}
	}
	return nil
}

// obtiene todas la networks del tipo de la whiteList
func (ig *IfacesGuard) getWhiteListInterfaces() (map[string]*netInterface, error) {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, fmt.Errorf("error obteniendo las interfaces de red: %w", err)
	}
	// interfaces obtenidas con NetworkManager
	nmcliIfaces, err := ig.getNetworkStates()
	if err != nil {
		return nil, err
	}
	whiteListIfaces := make(map[string]*netInterface)
	// Iterar por todas las interfaces
	for _, iface := range interfaces {
		if ifaceInfo, found := nmcliIfaces[iface.Name]; found {
			if slices.Contains(ig.IfacesTracked[:], ifaceInfo.IfaceType) {
				ifaceInfo.MACAddress = iface.HardwareAddr.String()
				whiteListIfaces[iface.Name] = ifaceInfo

			}
		}
	}
	return whiteListIfaces, nil
}

func (ig *IfacesGuard) getNetworkStates() (map[string]*netInterface, error) {
	cmd := exec.Command("nmcli", "-t", "-f", "DEVICE,TYPE,STATE", "device", "status")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("error ejecutando nmcli: %w", err)
	}
	ifaces := make(map[string]*netInterface)

	// Procesar la salida de nmcli
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		fields := strings.Split(line, ":")
		if len(fields) >= 3 {
			interfaceName := fields[0]         // Nombre de la interfaz
			ifaceType := fields[1]             // Tipo de la interfaz (ethernet, wifi, etc.)
			state := getCustomState(fields[2]) // Estado de la interfaz (connected, disconnected, etc.)
			newIface := &netInterface{
				Timestamp: time.Now().UnixMilli(),
				Interface: interfaceName,
				State:     state,
				IfaceType: ifaceType,
			}
			ifaces[interfaceName] = newIface
		}
	}

	return ifaces, nil
}

//funcion para mapear los estados.
/** estados reales encontrados hasta ahora: (separados por "|")
unavailable | connecting (checking IP connectivity) | connected | connecting (getting IP configuration) | disconnected | connecting (prepare)
**/
func getCustomState(realState string) (mappedState string) {
	mappedState = realState
	if realState == "unavailable" {
		mappedState = "disconnected"
	}
	return
}

func (ni *netInterface) TelegrafNormalize() system_utils.TelegrafEvent {
	tags := map[string]string{
		// "deviceID":   system_utils.GetUniqueID(),
		"group":      "IFACES",
		"ifaceType":  ni.IfaceType,
		"interface":  ni.Interface,
		"macAddress": ni.MACAddress,
	}
	fields := map[string]interface{}{
		"state": ni.State,
	}
	return system_utils.TelegrafEvent{
		Fields:   fields,
		Tags:     tags,
		DeviceID: system_utils.GetUniqueID(),
		Time:     time.UnixMilli(ni.Timestamp),
	}
}
func init() {
	inputs.Add("iface_guard", func() telegraf.Input {
		return &IfacesGuard{}
	})
}

package usb_guard

import (
	_ "embed"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/influxdata/telegraf"
	system_utils "github.com/influxdata/telegraf/plugins/common/system"
	"github.com/influxdata/telegraf/plugins/inputs"
	"github.com/pilebones/go-udev/netlink"
)

const (
	DISCONNECTED string = "disconnected"
	CONNECTED    string = "connected"
)

//go:embed sample.conf
var sampleConfig string

var eventsTimeout = 5 * time.Second

type UsbInfo struct {
	devId         string
	devName       string
	devType       string
	idModel       string
	idModelId     string
	idVendor      string
	idVendorId    string
	idSerial      string
	idSerialShort string
	idFsUuidEnc   string
}

type UsbsGuard struct {
	mutexEvents sync.Mutex
	acc         telegraf.Accumulator
	Log         telegraf.Logger `toml:"-"`
	usbCatcher
}
type usbCatcher struct {
	usbRulesMatcher netlink.Matcher
	kernelUsbConn   netlink.UEventConn
	quitChannel     chan struct{}
	eventsQueueChan chan usbsPluged
	eventsQueue     []netlink.UEvent
	queueTimer      *time.Timer
}
type usbsPluged struct {
	pluggedIn  map[string]*UsbDev
	pluggedOff map[string]*UsbDev
}
type UsbDev struct {
	Timestamp      int64  `json:"timestamp"`      //ts del ultimo evento
	State          string `json:"state"`          //(conectado/desconectado)
	ManufacturerId string `json:"manufacturerId"` //$ID_MODEL_ID:$ID_VENDOR_ID
	Interface      string `json:"interface"`      //nombre de la interfaz usb donde se engancha
	IdSerialName   string
	IdSerialShort  string
	IdFsUuidEnc    string
}

func (us *UsbsGuard) SampleConfig() string {
	return sampleConfig
}
func (us *UsbsGuard) Init() error {
	us.Log.Info("Usb events collect initialized")
	us.kernelUsbConn = netlink.UEventConn{}
	rules := &netlink.RuleDefinitions{}
	rules.AddRule(netlink.RuleDefinition{
		Env: map[string]string{
			"ID_USB_DRIVER": "usb-storage",
		},
	})
	us.usbRulesMatcher = rules
	return nil
}
func (us *UsbsGuard) Start(acc telegraf.Accumulator) error {
	us.acc = acc
	us.Log.Info("Usb events collect started")
	if err := us.kernelUsbConn.Connect(netlink.UdevEvent); err != nil {
		log.Fatalln("Unable to connect to Netlink Kobject UEvent socket")
	}
	queue := make(chan netlink.UEvent, 2)
	errors := make(chan error)
	us.eventsQueueChan = make(chan usbsPluged, 10)
	us.quitChannel = us.kernelUsbConn.Monitor(queue, errors, us.usbRulesMatcher)
	go us.manageEventQueue()
	go func() {
		for {
			select {
			case uvent := <-queue:
				us.mutexEvents.Lock()
				// log.Println("Device event input: ", uvent.Action)
				if us.queueTimer == nil {
					us.queueTimer = time.AfterFunc(eventsTimeout, func() {
						us.mutexEvents.Lock()
						now := time.Now()
						data := usbsPluged{pluggedIn: make(map[string]*UsbDev), pluggedOff: make(map[string]*UsbDev)}
						for _, ev := range us.eventsQueue {
							manufacturerId := fmt.Sprintf("%v:%v", ev.Env["ID_MODEL_ID"], ev.Env["ID_VENDOR_ID"])
							devIface := strings.Replace(ev.Env["DEVNAME"], "/dev/", "", 1)
							if ev.Action == netlink.ADD {
								newDevicePlugin := &UsbDev{
									Timestamp:      now.UnixMilli(),
									State:          CONNECTED,
									ManufacturerId: manufacturerId,
									Interface:      devIface,
									IdSerialName:   ev.Env["ID_SERIAL"],
									IdSerialShort:  ev.Env["ID_SERIAL_SHORT"],
									IdFsUuidEnc:    ev.Env["ID_FS_UUID_ENC"],
								}
								data.pluggedIn[devIface] = newDevicePlugin
							} else if ev.Action == netlink.REMOVE {
								newDevicePlugOff := &UsbDev{
									Timestamp:      now.UnixMilli(),
									State:          DISCONNECTED,
									ManufacturerId: manufacturerId,
									Interface:      devIface,
									IdSerialName:   ev.Env["ID_SERIAL"],
									IdSerialShort:  ev.Env["ID_SERIAL_SHORT"],
									IdFsUuidEnc:    ev.Env["ID_FS_UUID_ENC"],
								}
								data.pluggedOff[devIface] = newDevicePlugOff
							}
						}
						//enviar los dispositivos
						us.eventsQueue = []netlink.UEvent{}
						us.queueTimer = nil
						us.mutexEvents.Unlock()
						us.eventsQueueChan <- data
					})

				} else {
					us.queueTimer.Reset(eventsTimeout)
				}
				us.eventsQueue = append(us.eventsQueue, uvent)
				us.mutexEvents.Unlock()
			case err := <-errors:
				us.Log.Error("error: ", err)
			}
		}
	}()

	return nil
}

func (us *UsbsGuard) manageEventQueue() {
	for events := range us.eventsQueueChan {
		usbsPlugOff := events.pluggedOff
		usbsPlugIn := events.pluggedIn
		if len(usbsPlugOff) != 0 {
			if usbsMetricsOff := parseRawUsbToCompact(usbsPlugOff, DISCONNECTED, true); len(usbsPlugOff) != 0 {
				us.Log.Info("event usbs removed: ")
				for _, sysMetric := range usbsMetricsOff {
					me := sysMetric.TelegrafNormalize()
					us.Log.Infof("id: %v | iface: %v | state: %v | ts: %v\n", me.GetTags()["id"], me.GetTags()["devnames"], me.Fields["state"], me.GetTime())
					us.acc.AddFields(me.GetDeviceID(), me.GetFields(), me.GetTags(), me.GetTime())
				}
			}
		}
		if len(usbsPlugIn) != 0 {
			if usbsMetricsIn := parseRawUsbToCompact(usbsPlugIn, CONNECTED, true); len(usbsMetricsIn) != 0 {
				fmt.Println()
				us.Log.Info("event usbs plugin: ")
				for _, sysMetric := range usbsMetricsIn {
					me := sysMetric.TelegrafNormalize()
					us.Log.Infof("id: %v | iface: %v | state: %v | ts: %v\n", me.GetTags()["id"], me.GetTags()["devnames"], me.Fields["state"], me.GetTime())
					us.acc.AddFields(me.GetDeviceID(), me.GetFields(), me.GetTags(), me.GetTime())
				}
			}
		}
	}
}

func (us *UsbsGuard) Gather(_ telegraf.Accumulator) error {
	return nil
}
func (us *UsbsGuard) Stop() {
	close(us.quitChannel)
	us.kernelUsbConn.Close()
	us.Log.Info("usb monitor stopped")
}

func parseRawUsbToCompact(rawDevices map[string]*UsbDev, status string, removeInMap bool) (finalCompatUsbs map[string]*UsbDev) {
	finalUsbGrouped := make(map[string][]*UsbDev)
	finalCompatUsbs = make(map[string]*UsbDev)
	var deviceAlreadyChecked []string
	for iKey, i := range rawDevices {
		if slices.Contains(deviceAlreadyChecked, iKey) {
			continue
		}
		if (i.IdSerialShort == "" && i.IdFsUuidEnc == "") || i.IdSerialName == "" || i.ManufacturerId == "" {
			continue
		}
		finalUsbGrouped[iKey] = append(finalUsbGrouped[iKey], i)
		for kKey, k := range rawDevices {
			if kKey == iKey {
				continue
			}
			if i.IdSerialName == k.IdSerialName && i.ManufacturerId == k.ManufacturerId && (i.IdFsUuidEnc == k.IdFsUuidEnc || i.IdSerialShort == k.IdSerialShort) {
				finalUsbGrouped[iKey] = append(finalUsbGrouped[iKey], k)
				deviceAlreadyChecked = append(deviceAlreadyChecked, kKey)
				if removeInMap {
					delete(rawDevices, kKey)
				}
			}
		}
	}
	for _, usbsWithSameId := range finalUsbGrouped {
		ts := usbsWithSameId[0].Timestamp
		var idFs string
		var idSerialShort string
		var ifaces []string
		manufacturerId := usbsWithSameId[0].ManufacturerId
		idSerialName := usbsWithSameId[0].IdSerialName

		for _, usb := range usbsWithSameId {
			// fmt.Printf("Device: %v | idSerialName %v | idSerialShort %v | idFs %v | Manu: %v | Ts: %v\n", usb.Interface, usb.IdSerialName, usb.IdSerialShort, usb.IdFsUuidEnc, usb.ManufacturerId, usb.Timestamp)
			if usb.Timestamp < ts {
				ts = usb.Timestamp
			}
			if usb.IdFsUuidEnc != "" {
				idFs = usb.IdFsUuidEnc
			}
			if usb.IdSerialShort != "" {
				idSerialShort = usb.IdSerialShort
			}
			ifaces = append(ifaces, usb.Interface)
			sort.Strings(ifaces)
		}
		usb := &UsbDev{
			Timestamp:      ts,
			State:          status,
			ManufacturerId: manufacturerId,
			IdSerialName:   idSerialName,
			IdSerialShort:  idSerialShort,
			IdFsUuidEnc:    idFs,
			Interface:      strings.Join(ifaces, ":"),
		}
		finalCompatUsbs[strings.Join(ifaces, ":")] = usb
	}
	return
}

func (u *UsbDev) TelegrafNormalize() system_utils.TelegrafEvent {
	devUiid := fmt.Sprintf("%v:%v", u.IdSerialShort, u.IdFsUuidEnc)
	tags := map[string]string{
		"group":        "USBS",
		"id":           devUiid,
		"devnames":     u.Interface,
		"manufacturer": u.ManufacturerId,
	}
	fields := map[string]interface{}{
		"state": u.State,
	}
	return system_utils.TelegrafEvent{
		Fields:   fields,
		Tags:     tags,
		DeviceID: system_utils.GetUniqueID(),
		Time:     time.UnixMilli(u.Timestamp),
	}
}

func init() {
	inputs.Add("usb_guard", func() telegraf.Input {
		return &UsbsGuard{}
	})
}
func print(usbs map[string]*UsbDev) {
	for _, d := range usbs {
		log.Printf("Device: %v | idSerialName %v | idSerialShort %v | idFs %v | Manu: %v | Ts: %v\n", d.Interface, d.IdSerialName, d.IdSerialShort, d.IdFsUuidEnc, d.ManufacturerId, d.Timestamp)
	}
}

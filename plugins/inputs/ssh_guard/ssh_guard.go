package ssh_guard

import (
	"bufio"
	_ "embed"
	"fmt"
	"log"
	"math"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/influxdata/telegraf"
	system_utils "github.com/influxdata/telegraf/plugins/common/system"
	"github.com/influxdata/telegraf/plugins/inputs"
	pcap "github.com/packetcap/go-pcap"
)

type eventType string

var (
	NEW_SSH_NEW_CONN             eventType = "conn"
	NEW_SSH_LOGIN                eventType = "login"
	NEW_SSH_FAILED_LOGIN_ATTEMPT eventType = "login_attempt_fail"
	NEW_SSH_FINAL_LOGIN_FAILED   eventType = "login_fail"
	NEW_SSH_LOGOUT               eventType = "logout"

	SSH_SESSION_RX_BYTES eventType = "ssh_session_rx_bytes"
	SSH_SESSION_TX_BYTES eventType = "ssh_session_tx_bytes"
)

var (
	MONITOR_SSH_LOGIN_BASE_TIME  time.Duration = time.Duration(20) * time.Millisecond
	MONITOR_SSH_LOGIN_LIMIT_TIME time.Duration = time.Duration(5) * time.Second
)

//go:embed sample.conf
var sampleConfig string

var defaultSshPort uint16 = 22

const auditLogPath string = "/var/log/auth.log"
const auditLogTimeLayout string = "Jan 2 15:04:05 2006"

// Estructura para estadísticas de cada sesión SSH
type sshSession struct {
	bytesSent uint64
	bytesRecv uint64
	user      string
	ip        string
	port      string
	tsMs      int64
	event     eventType
	delete    bool //flag que se activa cuando la sesion esta lista para borrarse
}

type SshGuard struct {
	InterfaceTracked    string `toml:"interfaceTracked"`
	SshListenPort       uint16 `toml:"sshListenPort"`
	IntervalRateSeconds uint64 `toml:"intervalRateSeconds"`
	//solo sshSessions de bytes enviados y recibidos. Los eventos de sesion no se almacenaran en memoria, sino que se enviara inmediatamente al producirse
	sshSessions    map[string]*sshSession //key = ip:port
	mutex          sync.Mutex
	localIpInIface string
	sshSnifer      *pcap.Handle
	Log            telegraf.Logger `toml:"-"`
	accumulator    telegraf.Accumulator
}

func newSshSesion(user, ip, port string, event eventType, tsMs int64) *sshSession {
	return &sshSession{tsMs: tsMs, user: user, ip: ip, port: port, event: event}
}
func (s *sshSession) TelegrafNormalize() system_utils.TelegrafEvent {
	fields := make(map[string]interface{})
	tags := map[string]string{
		// "deviceID":  system_utils.GetUniqueID(),
		"ip":        s.ip,
		"port":      s.port,
		"group":     "SSH", //FIJO POR PLUGIN
		"eventType": string(s.event),
	}
	if s.user == "" {
		s.user = "unknown"
	}
	tags["user"] = s.user
	if s.event == SSH_SESSION_RX_BYTES {
		fields["connbytes"] = s.bytesRecv
	} else if s.event == SSH_SESSION_TX_BYTES {
		fields["connbytes"] = s.bytesSent
	} else {
		fields["authevent"] = 1
	}

	return system_utils.TelegrafEvent{
		Fields:   fields,
		Tags:     tags,
		DeviceID: system_utils.GetUniqueID(),
		Time:     time.UnixMilli(s.tsMs),
	}
}
func (ss *SshGuard) SampleConfig() string {
	return sampleConfig
}
func (ss *SshGuard) Init() error {
	ss.Log.Info("ssh events monitor started")
	ss.sshSessions = getActiveSSHSessions()
	if ss.SshListenPort == 0 {
		ss.SshListenPort = defaultSshPort
	}
	localIp, err := getIPFromInterface(ss.InterfaceTracked)
	if err != nil {
		ss.Log.Error(err)
		return err
	}
	//TODO ahora mismo esta localIpSeria siempre la que se lee al principio. Que pasa si se cae la red y se asigna otra ip a la interface?
	ss.localIpInIface = localIp
	for id, session := range ss.sshSessions {
		ss.Log.Infof("sessionId: %v | ip: %v | port: %v | user: %v\n", id, session.ip, session.port, session.user)
	}
	return nil
}
func (ss *SshGuard) Start(acc telegraf.Accumulator) error {
	ss.accumulator = acc
	go ss.checkBytesStatistics()
	go ss.monitorSshLogin()
	go ss.snifferSshTraffic()
	ss.Log.Info("ssh sniffer started")
	return nil
}
func (ss *SshGuard) Gather(acc telegraf.Accumulator) error {
	return nil
}

func (ss *SshGuard) Stop() {
	ss.sshSnifer.Close()
}
func (ss *SshGuard) checkBytesStatistics() {
	for {
		time.Sleep(time.Duration(ss.IntervalRateSeconds) * time.Second)
		ss.Log.Info("\n---- Estadísticas de sesiones SSH ----")
		now := time.Now()
		ss.mutex.Lock()
		for session, stats := range ss.sshSessions {
			var rxMetric, txMetric system_utils.SystemMetric
			if stats.bytesRecv == 0 && stats.bytesSent == 0 {
				if stats.delete {
					delete(ss.sshSessions, fmt.Sprintf("%v:%v", stats.ip, stats.port))
				}
				continue
			}
			rxMetric = &sshSession{
				ip:        stats.ip,
				port:      stats.port,
				user:      stats.user,
				tsMs:      now.UnixMilli(),
				bytesRecv: stats.bytesRecv,
				event:     SSH_SESSION_RX_BYTES,
			}
			txMetric = &sshSession{
				ip:        stats.ip,
				port:      stats.port,
				user:      stats.user,
				tsMs:      now.UnixMilli(),
				bytesSent: stats.bytesSent,
				event:     SSH_SESSION_TX_BYTES,
			}
			me_rx := rxMetric.TelegrafNormalize()
			me_tx := txMetric.TelegrafNormalize()
			ss.accumulator.AddFields(me_rx.DeviceID, me_rx.Fields, me_rx.Tags, me_rx.GetTime())
			ss.accumulator.AddFields(me_tx.DeviceID, me_tx.Fields, me_tx.Tags, me_tx.Time)
			ss.Log.Infof("Sesión %v -> Bytes Recibidos: %v | Bytes Enviados: %v\n", session, formatLogBytes(stats.bytesRecv), formatLogBytes(stats.bytesSent))
			if stats.delete {
				delete(ss.sshSessions, fmt.Sprintf("%v:%v", stats.ip, stats.port))
				continue
			}
			stats.bytesRecv = 0
			stats.bytesSent = 0
		}
		ss.mutex.Unlock()
		ss.Log.Info("--------------------------------------")
	}
}
func (ss *SshGuard) monitorSshLogin() {
	// now := time.Now()
	file, err := os.Open(auditLogPath)
	if err != nil {
		panic(fmt.Sprintf("Error abriendo log: %v", err))
	}
	defer file.Close()

	reader := bufio.NewReader(file)
	file.Seek(0, 2) // Ir al final del archivo

	// Expresiones regulares para capturar eventos
	reConnection := regexp.MustCompile(`Connection from (\d+\.\d+\.\d+\.\d+) port (\d+) on (\d+\.\d+\.\d+\.\d+) port (\d+)`)
	reFailedLogin := regexp.MustCompile(`Failed (password|none) for (invalid user )?(\S+) from (\d+\.\d+\.\d+\.\d+) port (\d+)`)
	reSuccessfulLogin := regexp.MustCompile(`Accepted password for (\S+) from (\d+\.\d+\.\d+\.\d+) port (\d+)`)
	//desconexion para todos los casos menos para cierre abrupto cliente ¿terminus?
	reDisconnected := regexp.MustCompile(`Disconnected from user (\S+) (\d+\.\d+\.\d+\.\d+) port (\d+)`)
	reClosed := regexp.MustCompile(`Connection closed by (\d+\.\d+\.\d+\.\d+) port (\d+)`)
	reFinalFailedLogin := regexp.MustCompile(`Connection closed by authenticating user (\S+) (\d+\.\d+\.\d+\.\d+) port (\d+) \[preauth\]`)
	// Caso: "error: maximum authentication attempts exceeded ... [preauth]" => fallo definitivo
	reMaxAuthExceeded := regexp.MustCompile(`error: maximum authentication attempts exceeded for (?:invalid user )?(\S+) from (\d+\.\d+\.\d+\.\d+) port (\d+)(?:\s+ssh2)? \[preauth\]`)
	reTime := regexp.MustCompile(`^((?:\w{3}\s+\d{1,2}\s+\d{2}:\d{2}:\d{2})|(?:\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:\+\d{2}:\d{2})?))`)

	ss.Log.Info("Monitorizando accesos SSH...")
	noEventCount := 0

	for {
		line, err := reader.ReadString('\n')
		// log.Println("Tiempo: ", time.Since(now))
		// now = time.Now()
		if err == nil {
			noEventCount = 0
			var newMetric system_utils.SystemMetric
			matches := reTime.FindStringSubmatch(line)
			if len(matches) >= 2 {
				logTime := matches[1] // Hora extraída, por ejemplo "Feb 13 14:55:12"
				tsMs := getTsFromAuditLog(logTime)

				// Detección de conexión SSH (antes del login)
				if matches := reConnection.FindStringSubmatch(line); len(matches) == 5 {
					ip := matches[1]
					port := matches[2]
					newMetric = newSshSesion("", ip, port, NEW_SSH_NEW_CONN, tsMs)
					ss.Log.Infof("[%s] Nueva conexión SSH establecida: Cliente='%s:%s' \n", logTime, ip, port)
				} else if matches := reFailedLogin.FindStringSubmatch(line); len(matches) == 6 {
					user := matches[3]
					ip := matches[4]
					port := matches[5]
					newMetric = newSshSesion(user, ip, port, NEW_SSH_FAILED_LOGIN_ATTEMPT, tsMs)
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					ss.mutex.Lock()
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.user = user
					} else {
						ss.sshSessions[sessionKey] = &sshSession{
							ip:   strings.Split(sessionKey, ":")[0],
							port: strings.Split(sessionKey, ":")[1],
							user: user,
						}
					}
					ss.mutex.Unlock()
					//asignar usuario
					ss.Log.Infof("[%v] Intento fallido de login: Usuario='%s' IP='%s' Puerto='%s'\n", logTime, user, ip, port)
				} else if matches := reFinalFailedLogin.FindStringSubmatch(line); len(matches) == 4 {
					user := matches[1]
					ip := matches[2]
					port := matches[3]
					newMetric = newSshSesion(user, ip, port, NEW_SSH_FINAL_LOGIN_FAILED, tsMs)
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					ss.mutex.Lock()
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.delete = true
					}
					ss.mutex.Unlock()
					//proponer eliminar usuario
					ss.Log.Infof("[%s] Login fallido definitivo: Usuario='%s' IP='%s' Puerto='%s' (Conexión cerrada)\n", logTime, user, ip, port)
				} else if matches := reMaxAuthExceeded.FindStringSubmatch(line); len(matches) == 4 {
					user := matches[1]
					ip := matches[2]
					port := matches[3]
					newMetric = newSshSesion(user, ip, port, NEW_SSH_FINAL_LOGIN_FAILED, tsMs)
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					ss.mutex.Lock()
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.delete = true
					}
					ss.mutex.Unlock()
					ss.Log.Infof("[%s] Login fallido definitivo: Usuario='%s' IP='%s' Puerto='%s' (Máximo de intentos excedido)\n", logTime, user, ip, port)
				} else if matches := reSuccessfulLogin.FindStringSubmatch(line); len(matches) == 4 {
					user := matches[1]
					ip := matches[2]
					port := matches[3]
					newMetric = newSshSesion(user, ip, port, NEW_SSH_LOGIN, tsMs)
					ss.mutex.Lock()
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.user = user
					}
					ss.mutex.Unlock()
					//asignar usuario
					ss.Log.Infof("[+] Inicio de sesión SSH exitoso: Usuario='%s' IP='%s' Puerto='%s'\n", user, ip, port)
				} else if matches := reDisconnected.FindStringSubmatch(line); len(matches) == 4 {
					user := matches[1]
					ip := matches[2]
					port := matches[3]
					newMetric = newSshSesion(user, ip, port, NEW_SSH_LOGOUT, tsMs)
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					ss.mutex.Lock()
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.delete = true
					}
					ss.mutex.Unlock()
					//proponer eliminar usuario. Lo hacemos asi para no eliminar un usuario aqui y luego no mandar el trafico en el ultimo tramo
					ss.Log.Infof("[-] Usuario desconectado: Usuario='%s' IP='%s' Puerto='%s'\n", user, ip, port)
				} else if matches := reClosed.FindStringSubmatch(line); len(matches) == 3 {
					//esta caso es una desconexion pero sin saber el user. Asi que lo sacamos del mapa
					ip := matches[1]
					port := matches[2]
					var user string
					sessionKey := fmt.Sprintf("%v:%v", ip, port)
					ss.mutex.Lock()
					if session, exists := ss.sshSessions[sessionKey]; exists {
						session.delete = true
						//se obtiene en este caso el user del mapa en memoria ya que no viene en el match
						user = session.user
					}
					ss.mutex.Unlock()
					newMetric = newSshSesion(user, ip, port, NEW_SSH_LOGOUT, tsMs)
					ss.Log.Infof("[--] Usuario desconectado: Usuario='%s' IP='%s' Puerto='%s'\n", user, ip, port)
				}
				if newMetric != nil {
					telEvent := newMetric.TelegrafNormalize()
					ss.accumulator.AddFields(telEvent.GetDeviceID(), telEvent.GetFields(), telEvent.GetTags(), telEvent.GetTime())
				}
			}
			time.Sleep(MONITOR_SSH_LOGIN_BASE_TIME)
			continue
		}
		noEventCount++
		factor := 5.0 // Ajusta este valor para cambiar la velocidad de crecimiento
		delayFloat := float64(MONITOR_SSH_LOGIN_BASE_TIME) * math.Log1p(float64(noEventCount)*factor)
		// delayFloat := float64(MONITOR_SSH_LOGIN_BASE_TIME) * math.Log1p(float64(noEventCount))
		if delayFloat < float64(MONITOR_SSH_LOGIN_BASE_TIME) {
			delayFloat = float64(MONITOR_SSH_LOGIN_BASE_TIME)
		}
		if delayFloat > float64(MONITOR_SSH_LOGIN_LIMIT_TIME) {
			delayFloat = float64(MONITOR_SSH_LOGIN_LIMIT_TIME)
		}
		time.Sleep(time.Duration(delayFloat))
	}

}
func (ss *SshGuard) snifferSshTraffic() {
	// monitorSSHTraffic captura paquetes SSH y actualiza estadísticas
	handle, err := pcap.OpenLive(ss.InterfaceTracked, 1600, true, 0, true)
	if err != nil {
		log.Fatalf("Error al abrir la interfaz: %v", err)
	}
	ss.sshSnifer = handle

	// Filtrar tráfico en el puerto 22
	if err := handle.SetBPFFilter(fmt.Sprintf("tcp port %d", ss.SshListenPort)); err != nil {
		log.Fatalf("Error al establecer filtro BPF: %v", err)
	}

	ss.Log.Infof("Monitorizando tráfico SSH en %s (actualización cada %d seg)...\n", ss.InterfaceTracked, ss.IntervalRateSeconds)

	packetSource := gopacket.NewPacketSource(handle, layers.LinkType(handle.LinkType()))

	for packet := range packetSource.Packets() {
		networkLayer := packet.NetworkLayer()
		transportLayer := packet.TransportLayer()

		if networkLayer == nil || transportLayer == nil {
			continue
		}

		// Extraer direcciones IP y puertos
		ip, _ := networkLayer.(*layers.IPv4)
		tcp, _ := transportLayer.(*layers.TCP)
		if ip == nil || tcp == nil {
			if ip == nil {
				ss.Log.Warn("ip is nil")
			}
			if tcp == nil {
				ss.Log.Warn("tcp is nil")
			}
			continue
		}

		sessionKey := ss.getKeyMap(ip, tcp)
		if sessionKey == "" {
			continue
		}

		ss.mutex.Lock()
		if _, exists := ss.sshSessions[sessionKey]; !exists {
			ss.sshSessions[sessionKey] = &sshSession{
				ip:   strings.Split(sessionKey, ":")[0],
				port: strings.Split(sessionKey, ":")[1],
			}
		}

		packetSize := uint64(packet.Metadata().CaptureLength)

		// Determinar si el tráfico es de entrada o salida
		if tcp.SrcPort == layers.TCPPort(ss.SshListenPort) {
			ss.sshSessions[sessionKey].bytesSent += packetSize
		} else if tcp.DstPort == layers.TCPPort(ss.SshListenPort) {
			ss.sshSessions[sessionKey].bytesRecv += packetSize
		} else {
			ss.Log.Infof("unknown traffic from: src: %v:%v | dst: %v:%v \n", ip.SrcIP.String(), tcp.SrcPort.String(), ip.DstIP.String(), tcp.DstPort.String())
			continue
		}
		ss.mutex.Unlock()
	}

}

// localIface: 192.168.1.96 | listenerPort: 22 scrIp:Port = 192.168.1.89:22(ssh) | dstIp:Port = 192.168.1.202:40866
func (ss *SshGuard) getKeyMap(ip *layers.IPv4, tcp *layers.TCP) string {
	if ss.localIpInIface == ip.SrcIP.String() && tcp.SrcPort == layers.TCPPort(ss.SshListenPort) {
		return fmt.Sprintf("%v:%v", ip.DstIP.String(), tcp.DstPort.String())
	} else if ss.localIpInIface == ip.DstIP.String() {
		return fmt.Sprintf("%v:%v", ip.SrcIP.String(), tcp.SrcPort.String())
	} else {
		//trafico para ssh en otra interfaz de red/por lo tanto otra ip
		return ""
	}
}

func formatLogBytes(bytes uint64) string {
	const unit = 1024
	sizes := []string{"B", "KiB", "MiB", "GiB", "TiB", "PiB", "EiB"}

	bytesFloat := float64(bytes)
	i := 0
	for bytesFloat >= unit && i < len(sizes)-1 {
		bytesFloat /= unit
		i++
	}
	return fmt.Sprintf("%.2f%s", bytesFloat, sizes[i])
}
func getIPFromInterface(ifaceName string) (string, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return "", fmt.Errorf("error obteniendo la interfaz: %v", err)
	}

	addrs, err := iface.Addrs()
	if err != nil {
		return "", fmt.Errorf("error obteniendo las direcciones: %v", err)
	}

	for _, addr := range addrs {
		switch v := addr.(type) {
		case *net.IPNet:
			if !v.IP.IsLoopback() && v.IP.To4() != nil {
				return v.IP.String(), nil
			}
		}
	}

	return "", fmt.Errorf("no se encontró una dirección IPv4 válida en la interfaz %s", ifaceName)
}

// getActiveSSHSessions obtiene las sesiones SSH activas
func getActiveSSHSessions() (sessions map[string]*sshSession) {
	sessions = make(map[string]*sshSession)

	// Ejecutar 'netstat -antp | grep sshd' para obtener IP, puerto y usuario
	netstatCmd := exec.Command("bash", "-c", "netstat -antp | grep 'sshd:'")
	netstatOut, err := netstatCmd.Output()
	if err != nil {
		log.Println("Error ejecutando 'netstat':", err)
		return sessions
	}

	netstatScanner := bufio.NewScanner(strings.NewReader(string(netstatOut)))
	sshdRegex := regexp.MustCompile(`tcp\s+\d+\s+\d+\s+(\d+\.\d+\.\d+\.\d+):(\d+)\s+(\d+\.\d+\.\d+\.\d+):(\d+)\s+ESTABLISHED\s+\d+/sshd: (\S+)`) // Extrae IP, puerto y usuario

	for netstatScanner.Scan() {
		matches := sshdRegex.FindStringSubmatch(netstatScanner.Text())
		if len(matches) == 6 {
			ip := matches[3]
			port := matches[4]
			user := matches[5]
			idSession := fmt.Sprintf("%v:%v", ip, port)
			sessions[idSession] = &sshSession{user: user, ip: ip, port: port}
		}
	}
	return sessions
}

func getTsFromAuditLog(auditTsStr string) int64 {
	now := time.Now()
	if auditTsStr == "" {
		return now.UnixMilli()
	}

	// Formatos que vamos a soportar
	layoutClassic := "Jan 2 15:04:05.000 2006"
	layoutISO := "2006-01-02T15:04:05.000000-07:00"
	layoutISOshort := "2006-01-02T15:04:05-07:00"

	location := now.Location()
	millis := now.Nanosecond() / 1_000_000

	// Ver si es formato clásico (comienza con 3 letras del mes)
	if matched, _ := regexp.MatchString(`^[A-Z][a-z]{2} \s?\d{1,2} \d{2}:\d{2}:\d{2}`, auditTsStr); matched {
		year := now.Year()
		fullDate := fmt.Sprintf("%s.%03d %d", auditTsStr, millis, year)
		if parsedTime, err := time.ParseInLocation(layoutClassic, fullDate, location); err == nil {
			return parsedTime.UnixMilli()
		}
	}

	// Intentar parsear como ISO con microsegundos
	if parsedTime, err := time.Parse(layoutISO, auditTsStr); err == nil {
		return parsedTime.UnixMilli()
	}

	// Intentar parsear como ISO sin microsegundos
	if parsedTime, err := time.Parse(layoutISOshort, auditTsStr); err == nil {
		return parsedTime.UnixMilli()
	}

	// Fallback si todo falla
	return now.UnixMilli()
}

func init() {
	inputs.Add("ssh_guard", func() telegraf.Input {
		return &SshGuard{}
	})
}

//go:build !custom || inputs || inputs.iface_guard

package all

import _ "github.com/influxdata/telegraf/plugins/inputs/iface_guard" // register plugin
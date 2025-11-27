//go:build !custom || inputs || inputs.usb_guard

package all

import _ "github.com/influxdata/telegraf/plugins/inputs/usb_guard" // register plugin

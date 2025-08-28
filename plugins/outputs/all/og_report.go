//go:build !custom || outputs || outputs.og_report

package all

import _ "github.com/influxdata/telegraf/plugins/outputs/og_report" // register plugin

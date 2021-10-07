fork from https://github.com/temoto/venderctl
# What

Open source vending machine data processing server. The backend for https://github.com/temoto/vender

Goals:
- [cmd/tele] receive telemetry from Vender VMC
- [cmd/control] send remote control commands to vending machines
- [cmd/tax] send reports to government fiscal agency
- load telemetry data into existing legacy dashboard
- (maybe) new dashboard, alerts

Requires PostgreSQL 10+.

Please see [venderctl.hcl](venderctl.hcl) for config example. Except noted, defaults are Go zero values: 0, false, "".

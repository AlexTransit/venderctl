// default value 
// if bool not set then this = false
// if number not set then this = 0
// if string not set then this = ""

db {
  // github.com/go-pg/pg ParseURL
  url = "postgresql://user:password@server/database"
}

telegram {
 telegram_bot_api = "telegram bot api"
 telegram_admin = 0 //telegram user id 
 telegram_debug = true
 admin_bot_name = "name" // name admin bot, for verification if bots in joint channel"
 }


money {
  // Multiple of lowest money unit for config convenience and formatting.
  // All money numbers in config are multipled by scale.
  // For USD/EUR set `scale=1` to work with cents.
  scale = 100
}

tax {
  debug_level = 7 // maximum
  ru2019 {
    tag1009 = "payment address"
    tag1187 = "payment place"
    tag1018 = ""
    tag1055 = 2
    tag1199 = 6

    umka {
      base_url = "http://70:70@office.armax.ru:58088"
    }
  }
}

cashless {
  DebugLevel = 7 // if not set then maximum
  terminalkey = "terminal key"
  terminalPass = "terminal password"
  QRValidTimeSec = 300 // QR validet time. default 300
  terminalBankCommission = 40 // fixed point. 40 = 0.4%. default 0
  terminalMinimalAmount = 1000 // minimal amount. fixed point. default 1000 (10 rub.)
  terminalQRPayRefreshSec = 3 // payment verification. default 3  
  timeoutToStartManualPaymentVerificationSec = 20 // after how many seconds to start checking the payment status manually. default 20
  urlToListenToBankNotifications = "http://127.0.0.1:1234/bank/notiaicaion"
}

tele {
  // Only these variables passed as environment:
  // db_updated=true  if state successfully written to database
  // vmid=123         int32
  // new=2            integer, current state, see tele.proto enum State
  // prev=1           previous state, 0 means problem or just new vmid
  exec_on_state = "/usr/local/bin/vender-on-state"

  log_debug      = false // debug venderctl/tele
  mqtt_log_debug = false // debug MQTT library

  role_admin   = ["admin"]
  role_control = ["ctl"]
  role_monitor = ["prometheus"]
  secrets      = "/etc/venderctl/tele.secrets"

  listen "tls://[addr]:port" {
    allow_roles = ["control"]

    network_timeout_sec = 5

    tls {
      ca_file   = "/ca.pem"
      cert_file = "/server.pem"
      key_file  = "/server.key"
      psk       = ""            // secret
    }
  }

  listen "unix:///run/venderctl" {
    allow_roles         = ["_all"]
    network_timeout_sec = 1
  }

  connect {
    url                 = "tcp://ctl:secret@internal"
    network_timeout_sec = 5
    keepalive_sec = 10    
  }
}

include "venderctl-local.hcl" {
  optional = true
}

package main

import (
  "context"
  "errors"
  "net/url"
  "regexp"
  "strings"

  "control/internal/db"
)

// Keys of the settings rows. Every key an admin may write is listed in
// settingDefs below; a key that is not there cannot be written through the API,
// so a bug in the handler cannot turn into an arbitrary row insert.
const (
  settingAgentOpenEnrollment = "agent_open_enrollment"

  settingVectorVersion      = "vector_version"
  settingClickHouseURL      = "clickhouse_url"
  settingClickHouseUser     = "clickhouse_user"
  settingClickHousePassword = "clickhouse_password"
)

// settingKind says how a value is stored and rendered. The column is TEXT
// either way; this is what the API validates against and what the UI switches
// its control on.
type settingKind string

const (
  settingBool   settingKind = "bool"
  settingString settingKind = "string"
  // settingSecret is a string that is never read back. The value is writable
  // and usable server-side, but ListSettings returns it empty: an admin screen
  // that displays a credential turns every session with the tab open into a
  // place it can leak from.
  settingSecret settingKind = "secret"
)

// settingDef describes one operator-editable setting: what it is called in the
// UI, what setting it means, and what the server assumes when the row is
// missing.
type settingDef struct {
  Key         string      `json:"key"`
  Kind        settingKind `json:"kind"`
  Label       string      `json:"label"`
  Description string      `json:"description"`
  // Placeholder is shown in an empty text field, for settings whose format is
  // not obvious from the label.
  Placeholder string `json:"placeholder,omitempty"`
  // Warn marks a setting whose "on" state weakens security, so the UI can say
  // so rather than presenting every toggle as equivalent.
  Warn    bool   `json:"warn"`
  Default string `json:"-"`
  // validate rejects a value before it is stored. Nil means anything goes.
  validate func(string) error `json:"-"`
}

var settingDefs = []settingDef{
  {
    Key:   settingAgentOpenEnrollment,
    Kind:  settingBool,
    Label: "Open agent enrollment",
    Description: "Let a machine enroll itself with no enrollment key. " +
      "Anything that can reach the enrollment endpoint can then join the fleet, " +
      "so leave this off unless the control plane is on a network you own.",
    Warn:    true,
    Default: "false",
  },
  {
    Key:   settingVectorVersion,
    Kind:  settingString,
    Label: "Vector version",
    Description: "The vector release each agent downloads and runs to ship suricata's " +
      "events to clickhouse. Changing it re-installs vector on every host on its next " +
      "connect, so move it deliberately.",
    Placeholder: "0.57.0",
    Default:     "0.57.0",
    validate:    validateVectorVersion,
  },
  {
    Key:   settingClickHouseURL,
    Kind:  settingString,
    Label: "ClickHouse URL",
    Description: "The HTTP endpoint agents ship events to. It must be reachable from " +
      "every qemu host, not just from the control server. Empty stops vector from " +
      "being installed at all.",
    Placeholder: "http://10.68.0.1:8123",
    validate:    validateClickHouseURL,
  },
  {
    Key:         settingClickHouseUser,
    Kind:        settingString,
    Label:       "ClickHouse user",
    Description: "The account agents authenticate to clickhouse as.",
    Placeholder: "dummie",
    Default:     "dummie",
  },
  {
    Key:   settingClickHousePassword,
    Kind:  settingSecret,
    Label: "ClickHouse password",
    Description: "Written to /etc/vector/vector.yaml on every host, so it is only as " +
      "private as the least private machine in the fleet. Never shown again once saved.",
  },
}

// vectorVersionRe is deliberately strict. The value is interpolated into a
// github release URL that the agent downloads and installs as root, so anything
// that is not a plain release number has no business being accepted here.
var vectorVersionRe = regexp.MustCompile(`^\d+\.\d+\.\d+$`)

func validateVectorVersion(v string) error {
  if !vectorVersionRe.MatchString(v) {
    return errors.New("must be a release number like 0.57.0")
  }
  return nil
}

// validateClickHouseURL accepts an empty value -- that is how an operator turns
// event shipping off -- but anything else has to be a URL vector can dial.
func validateClickHouseURL(v string) error {
  if v == "" {
    return nil
  }
  u, err := url.Parse(v)
  if err != nil {
    return errors.New("must be a valid URL")
  }
  if u.Scheme != "http" && u.Scheme != "https" {
    return errors.New("must be an http or https URL")
  }
  if u.Host == "" {
    return errors.New("must include a host, e.g. http://10.0.0.1:8123")
  }
  return nil
}

func settingDefByKey(key string) (settingDef, bool) {
  for _, d := range settingDefs {
    if d.Key == key {
      return d, true
    }
  }
  return settingDef{}, false
}

// parseSettingBool reads the stored TEXT. Unrecognised values are false rather
// than an error: a hand-edited row must not be able to fail a request.
func parseSettingBool(v string) bool {
  switch strings.ToLower(strings.TrimSpace(v)) {
  case "1", "true", "yes", "on":
    return true
  }
  return false
}

func formatSettingBool(v bool) string {
  if v {
    return "true"
  }
  return "false"
}

// setting reads a setting's raw value from the database on every call -- these
// are single-row primary-key lookups, and caching them would mean an operator
// changing one has to wait for a restart, which is the whole reason these moved
// out of the environment.
//
// A missing row or a failed read falls back to the declared default. For
// settings that loosen a restriction, that default is the restrictive value, so
// a database the server cannot read never widens access.
func setting(ctx context.Context, q *db.Queries, key string) string {
  def, ok := settingDefByKey(key)
  if !ok {
    return ""
  }
  row, err := q.GetSetting(ctx, key)
  if err != nil {
    return def.Default
  }
  return row.Value
}

func boolSetting(ctx context.Context, q *db.Queries, key string) bool {
  return parseSettingBool(setting(ctx, q, key))
}

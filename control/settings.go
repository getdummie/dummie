package main

import (
  "context"
  "strings"

  "control/internal/db"
)

// Keys of the settings rows. Every key an admin may write is listed in
// settingDefs below; a key that is not there cannot be written through the API,
// so a bug in the handler cannot turn into an arbitrary row insert.
const settingAgentOpenEnrollment = "agent_open_enrollment"

// settingDef describes one operator-editable setting: what it is called in the
// UI, what turning it on means, and what the server assumes when the row is
// missing. All settings are booleans for now; the storage is TEXT, so widening
// this to other types later does not need a migration.
type settingDef struct {
  Key         string `json:"key"`
  Label       string `json:"label"`
  Description string `json:"description"`
  // Warn marks a setting whose "on" state weakens security, so the UI can say
  // so rather than presenting every toggle as equivalent.
  Warn    bool `json:"warn"`
  Default bool `json:"-"`
}

var settingDefs = []settingDef{
  {
    Key:   settingAgentOpenEnrollment,
    Label: "Open agent enrollment",
    Description: "Let a machine enroll itself with no enrollment key. " +
      "Anything that can reach the enrollment endpoint can then join the fleet, " +
      "so leave this off unless the control plane is on a network you own.",
    Warn:    true,
    Default: false,
  },
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

// boolSetting reads a setting from the database on every call -- these are
// single-row primary-key lookups, and caching them would mean an operator
// toggling a setting has to wait for a restart, which is the whole reason these
// moved out of the environment.
//
// A missing row or a failed read falls back to the declared default. For
// settings that loosen a restriction, that default is the restrictive value, so
// a database the server cannot read never widens access.
func boolSetting(ctx context.Context, q *db.Queries, key string) bool {
  def, ok := settingDefByKey(key)
  if !ok {
    return false
  }
  row, err := q.GetSetting(ctx, key)
  if err != nil {
    return def.Default
  }
  return parseSettingBool(row.Value)
}

// The vocabulary of an egress allowance, shared by the two places that compose
// one: the VM page's add-a-destination dialog and the new-VM dialog's starting
// allowlist. Both post to the same validator on the server, so the choices they
// offer and the body they build have to agree — kept here rather than copied,
// because the copy that drifted would be the one offering access the server then
// refuses, or worse, granting something other than what the label said.

/**
 * What a domain allowance can be checked on. Not a free port field: the ports
 * Suricata looks for http and tls on come from a per-host config, so a rule on
 * 8443 would load and never match. 'none' lets the name resolve and grants no
 * access at all.
 */
export const domainPortChoices = [
  { value: '80,443', label: 'https and http (443, 80)' },
  { value: '443', label: 'https only (443)' },
  { value: '80', label: 'http only (80)' },
  { value: 'none', label: 'lookup only — resolves, no access' },
]

/**
 * Presets for an address allowance. Presentation only: each expands to the
 * transport and ports the row actually stores, which stay visible and editable
 * rather than hidden behind the label.
 */
export const portPresets = [
  { key: 'ssh', label: 'SSH / SFTP (tcp 22)', transport: 'tcp', ports: '22' },
  { key: 'https', label: 'HTTPS (tcp 443)', transport: 'tcp', ports: '443' },
  { key: 'http', label: 'HTTP (tcp 80)', transport: 'tcp', ports: '80' },
  { key: 'postgres', label: 'PostgreSQL (tcp 5432)', transport: 'tcp', ports: '5432' },
  { key: 'mysql', label: 'MySQL (tcp 3306)', transport: 'tcp', ports: '3306' },
  { key: 'redis', label: 'Redis (tcp 6379)', transport: 'tcp', ports: '6379' },
  { key: 'smtp', label: 'SMTP submission (tcp 587)', transport: 'tcp', ports: '587' },
  { key: 'smtps', label: 'SMTPS (tcp 465)', transport: 'tcp', ports: '465' },
  { key: 'ntp', label: 'NTP (udp 123)', transport: 'udp', ports: '123' },
  { key: 'ftp', label: 'FTP control (tcp 21)', transport: 'tcp', ports: '21' },
  { key: 'custom', label: 'Custom…', transport: '', ports: '' },
]

/**
 * One destination as a form holds it.
 *
 * domainPorts is separate from ports because the same empty string means
 * opposite things to the two kinds — every port for an address, both web ports
 * for a name — so one field would carry a value that is wrong the moment the
 * type changes.
 */
export interface TargetForm {
  // 'domain' or 'ip'. A plain string, not a union: it is bound straight to a
  // Select, whose model value is not narrowed to our literals.
  kind: string
  destination: string
  transport: string
  ports: string
  domainPorts: string
  note: string
  preset: string
  /** Seconds. '0' is permanent; anything else is withdrawn that far from now. */
  ttl_seconds: string
}

export function blankTarget(): TargetForm {
  return {
    kind: 'domain',
    destination: '',
    transport: 'tcp',
    ports: '',
    domainPorts: '80,443',
    note: '',
    preset: 'custom',
    ttl_seconds: '0',
  }
}

/**
 * Switching type has to clear the ports. The two kinds accept disjoint values —
 * 'none' is meaningless on an address and '5432' is rejected on a domain — so a
 * value left over from the other kind is a validation error the user did not
 * type. Call this from the control's own change handler, never from a watcher:
 * a watcher flushes after the current call stack, so prefilling a form (setting
 * the kind, then the fields that go with it) would have the reset land
 * afterwards and wipe them.
 */
export function resetForKind(form: TargetForm) {
  form.ports = ''
  form.domainPorts = '80,443'
  form.transport = 'tcp'
  form.preset = 'custom'
}

/** Choosing a preset writes through to the fields that are actually stored. */
export function applyPreset(form: TargetForm, key: string) {
  const preset = portPresets.find(p => p.key === key)
  if (!preset || preset.key === 'custom') return
  form.transport = preset.transport
  form.ports = preset.ports
}

export function destinationPlaceholder(kind: string) {
  return kind === 'domain' ? 'ifconfig.io' : '1.1.1.1 or 10.0.0.0/8'
}

/**
 * Says what the row will actually compile to. The two kinds differ in a way the
 * field labels alone do not explain: a domain is matched by the name in the
 * traffic, and an address is matched by the rule header.
 */
export function matchSummary(kind: string) {
  return kind === 'domain'
    ? 'Matched by name — the TLS SNI or the HTTP host — and answered by the resolver. Only 443 and 80 can be checked this way.'
    : 'Matched by address in the rule header, with the transport and ports below. This is how anything that is not https or http is allowed.'
}

/**
 * Plain FTP is the one preset that does not describe a whole allowance: the
 * data channel lands on a port neither of us knows in advance.
 */
export function presetWarning(preset: string) {
  return preset === 'ftp'
    ? 'FTP moves data on a second, unpredictable port. Add the passive port range your server is configured for as another entry, or use SFTP, which needs only port 22.'
    : ''
}

/**
 * The request body, for both the add-one route and the targets array a create
 * carries. The one line worth sharing above all the others is which of the two
 * port fields becomes `ports`.
 */
export function toTargetPayload(form: TargetForm) {
  return {
    kind: form.kind,
    destination: form.destination,
    transport: form.transport,
    ports: form.kind === 'domain' ? form.domainPorts : form.ports,
    note: form.note,
    ttl_seconds: Number(form.ttl_seconds),
  }
}

/** A one-line description of an allowance, for reviewing a list of them. */
export function describeTarget(form: TargetForm) {
  if (form.kind === 'domain') {
    const choice = domainPortChoices.find(c => c.value === form.domainPorts)
    return choice?.label ?? form.domainPorts
  }
  if (form.transport === 'icmp') return 'ping'
  return `${form.transport} ${form.ports || 'any port'}`
}

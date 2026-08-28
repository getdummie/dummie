
export const domainPortChoices = [
  { value: '80,443', label: 'https and http (443, 80)' },
  { value: '443', label: 'https only (443)' },
  { value: '80', label: 'http only (80)' },
  { value: 'none', label: 'lookup only — resolves, no access' },
]

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

export interface TargetForm {
  kind: string
  destination: string
  transport: string
  ports: string
  domainPorts: string
  note: string
  preset: string
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

export function resetForKind(form: TargetForm) {
  form.ports = ''
  form.domainPorts = '80,443'
  form.transport = 'tcp'
  form.preset = 'custom'
}

export function applyPreset(form: TargetForm, key: string) {
  const preset = portPresets.find(p => p.key === key)
  if (!preset || preset.key === 'custom') return
  form.transport = preset.transport
  form.ports = preset.ports
}

export function destinationPlaceholder(kind: string) {
  return kind === 'domain' ? 'ifconfig.io' : '1.1.1.1 or 10.0.0.0/8'
}

export function matchSummary(kind: string) {
  return kind === 'domain'
    ? 'Matched by name — the TLS SNI or the HTTP host — and answered by the resolver. Only 443 and 80 can be checked this way.'
    : 'Matched by address in the rule header, with the transport and ports below. This is how anything that is not https or http is allowed.'
}

export function presetWarning(preset: string) {
  return preset === 'ftp'
    ? 'FTP moves data on a second, unpredictable port. Add the passive port range your server is configured for as another entry, or use SFTP, which needs only port 22.'
    : ''
}

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

export function describeTarget(form: TargetForm) {
  if (form.kind === 'domain') {
    const choice = domainPortChoices.find(c => c.value === form.domainPorts)
    return choice?.label ?? form.domainPorts
  }
  if (form.transport === 'icmp') return 'ping'
  return `${form.transport} ${form.ports || 'any port'}`
}

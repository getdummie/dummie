import { blankTarget, portPresets, toTargetPayload, type TargetForm } from '@/lib/targets'

export interface RecipeTarget {
  kind: string
  destination: string
  transport: string
  ports: string
  note: string
  ttl_seconds?: number
}

export interface RecipeIntegration {
  id: string
  repos?: string[]
}

// Every field is optional: whatever is missing takes the create form's default.
export interface RecipeConfig {
  osimage_id?: string
  kernel_id?: string
  cpus?: number
  memory_mib?: number
  disk_size?: string
  default_port?: number
  public_ports?: number[]
  ttl_seconds?: number
  init_script?: string
  targets?: RecipeTarget[]
  integrations?: RecipeIntegration[]
}

export interface Recipe {
  id: string
  name: string
  description: string
  config: RecipeConfig
  created_at: string
  updated_at: string
}

export function recipeTargetToForm(t: RecipeTarget): TargetForm {
  const form = blankTarget()
  form.kind = t.kind || 'domain'
  form.destination = t.destination
  form.note = t.note ?? ''
  form.ttl_seconds = String(t.ttl_seconds ?? 0)
  if (form.kind === 'domain') {
    form.domainPorts = t.ports || '80,443'
    return form
  }
  form.transport = t.transport || 'tcp'
  form.ports = t.ports
  form.preset = portPresets.find(p => p.transport === form.transport && p.ports === form.ports)?.key ?? 'custom'
  return form
}

export function formToRecipeTarget(f: TargetForm): RecipeTarget {
  const p = toTargetPayload(f)
  return { ...p, ttl_seconds: p.ttl_seconds || undefined }
}

// Lists what a recipe pins down, for a one-line summary.
export function recipeSummary(c: RecipeConfig): string[] {
  const out: string[] = []
  if (c.cpus) out.push(`${c.cpus} vCPU`)
  if (c.memory_mib) out.push(`${c.memory_mib} MiB`)
  if (c.disk_size) out.push(`${c.disk_size} disk`)
  if (c.default_port) out.push(`:${c.default_port}`)
  if (c.public_ports?.length) out.push(`public ${c.public_ports.join(', ')}`)
  if (c.targets?.length) out.push(`${c.targets.length} destination${c.targets.length === 1 ? '' : 's'}`)
  if (c.integrations?.length) out.push(`${c.integrations.length} integration${c.integrations.length === 1 ? '' : 's'}`)
  if (c.init_script) out.push('init script')
  if (c.ttl_seconds) out.push(`ttl ${c.ttl_seconds}s`)
  return out
}

import { ArrowLeftRight, Bot, FileCode, FileDiff, Globe, History, SquareTerminal } from '@lucide/vue'

export const GRID_COLS = 24
export const GRID_ROWS = 24

export type PanelType = 'chat' | 'console' | 'files' | 'transfer' | 'network' | 'diff' | 'sessions'

export interface GridItem {
  panel: PanelType
  x: number
  y: number
  w: number
  h: number
}

export interface WorkspaceLayout {
  id: string
  name: string
  builtin: boolean
  layout: { items: GridItem[] }
  created_at?: string
  updated_at?: string
}

export const panels: Record<PanelType, { label: string, icon: typeof Bot }> = {
  chat: { label: 'chat', icon: Bot },
  console: { label: 'console', icon: SquareTerminal },
  files: { label: 'files', icon: FileCode },
  transfer: { label: 'transfer', icon: ArrowLeftRight },
  network: { label: 'network', icon: Globe },
  diff: { label: 'diff', icon: FileDiff },
  sessions: { label: 'sessions', icon: History },
}

export const panelTypes = Object.keys(panels) as PanelType[]

type Rect = Pick<GridItem, 'x' | 'y' | 'w' | 'h'>

export function overlaps(a: Rect, b: Rect) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h
}

export function fits(r: Rect, items: GridItem[], skip?: PanelType) {
  return r.x >= 0 && r.y >= 0 && r.w >= 1 && r.h >= 1
    && r.x + r.w <= GRID_COLS && r.y + r.h <= GRID_ROWS
    && items.every(i => i.panel === skip || !overlaps(r, i))
}

export function readingOrder(items: GridItem[]) {
  return [...items].sort((a, b) => a.y - b.y || a.x - b.x)
}

function largestFree(items: GridItem[]): Rect | null {
  let best: Rect | null = null
  for (let y = 0; y < GRID_ROWS; y++) {
    for (let x = 0; x < GRID_COLS; x++) {
      for (let h = GRID_ROWS - y; h >= 1; h--) {
        for (let w = GRID_COLS - x; w >= 1; w--) {
          if (best && w * h <= best.w * best.h) break
          const r = { x, y, w, h }
          if (fits(r, items)) best = r
        }
      }
    }
  }
  return best
}

// Places a new panel in the largest free area or, when the grid is full,
// takes half of the largest panel that can be halved.
export function addPanel(items: GridItem[], panel: PanelType): GridItem[] | null {
  const free = largestFree(items)
  if (free) return [...items, { panel, ...free }]
  const host = [...items]
    .filter(i => i.w >= 2 || i.h >= 2)
    .sort((a, b) => b.w * b.h - a.w * a.h)[0]
  if (!host) return null
  const shrunk = { ...host }
  let added: GridItem
  if (host.w >= host.h) {
    shrunk.w = Math.ceil(host.w / 2)
    added = { panel, x: host.x + shrunk.w, y: host.y, w: host.w - shrunk.w, h: host.h }
  }
  else {
    shrunk.h = Math.ceil(host.h / 2)
    added = { panel, x: host.x, y: host.y + shrunk.h, w: host.w, h: host.h - shrunk.h }
  }
  return [...items.map(i => (i.panel === host.panel ? shrunk : i)), added]
}

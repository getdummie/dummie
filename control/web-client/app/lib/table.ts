import type { HTMLAttributes } from "vue"

export interface DataTableColumn {
  key: string
  label: string
  align?: "left" | "right"
  class?: HTMLAttributes["class"]
}

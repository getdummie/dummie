export interface ViewportPreset {
  label: string
  width: number
  height: number
}

export const desktopPresets: ViewportPreset[] = [
  { label: 'laptop · 1280×800', width: 1280, height: 800 },
  { label: 'desktop · 1440×900', width: 1440, height: 900 },
  { label: 'wide · 1920×1080', width: 1920, height: 1080 },
  { label: 'tablet · 1024×768', width: 1024, height: 768 },
]

export const mobilePresets: ViewportPreset[] = [
  { label: 'phone · 390×844', width: 390, height: 844 },
  { label: 'small · 375×667', width: 375, height: 667 },
  { label: 'large · 430×932', width: 430, height: 932 },
  { label: 'android · 412×915', width: 412, height: 915 },
]

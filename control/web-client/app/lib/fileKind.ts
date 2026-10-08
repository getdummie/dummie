export type MediaKind = 'image' | 'audio' | 'video'
export type PreviewKind = 'markdown' | 'svg' | 'html'

const media: Record<string, [MediaKind, string]> = {
  png: ['image', 'image/png'],
  jpg: ['image', 'image/jpeg'],
  jpeg: ['image', 'image/jpeg'],
  gif: ['image', 'image/gif'],
  webp: ['image', 'image/webp'],
  avif: ['image', 'image/avif'],
  bmp: ['image', 'image/bmp'],
  ico: ['image', 'image/x-icon'],
  mp3: ['audio', 'audio/mpeg'],
  wav: ['audio', 'audio/wav'],
  ogg: ['audio', 'audio/ogg'],
  oga: ['audio', 'audio/ogg'],
  opus: ['audio', 'audio/ogg'],
  flac: ['audio', 'audio/flac'],
  m4a: ['audio', 'audio/mp4'],
  aac: ['audio', 'audio/aac'],
  weba: ['audio', 'audio/webm'],
  mp4: ['video', 'video/mp4'],
  m4v: ['video', 'video/mp4'],
  webm: ['video', 'video/webm'],
  ogv: ['video', 'video/ogg'],
  mov: ['video', 'video/quicktime'],
  mkv: ['video', 'video/x-matroska'],
}

const previews: Record<string, PreviewKind> = { md: 'markdown', markdown: 'markdown', svg: 'svg', html: 'html', htm: 'html' }

function ext(path: string) {
  return path.slice(path.lastIndexOf('.') + 1).toLowerCase()
}

export function mediaKind(path: string): { kind: MediaKind, mime: string } | null {
  const hit = media[ext(path)]
  return hit ? { kind: hit[0], mime: hit[1] } : null
}

export function previewKind(path: string): PreviewKind | null {
  return previews[ext(path)] ?? null
}

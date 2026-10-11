import { extractPelicanSource, getPelicanAssessmentQuality } from './pelicanHtml'
import { createPelicanPreviewChannel, createPelicanPreviewDocument, fitPelicanArtwork, getPelicanViewport, readPelicanSizeMessage, type PelicanPreviewMode } from './pelicanPreview'

export interface PelicanRenderOptions {
  html: string
  title?: string
  mode?: PelicanPreviewMode
  interactive?: boolean
  quality?: 'normal' | 'degraded' | 'unknown' | null
  reason?: string
  badgeLabel?: string
}

// The site and API preview documents use this same renderer, without Vue or
// Tailwind dependencies. The caller owns the container's width and height.
export function mountPelicanArtwork(container: HTMLElement, initial: PelicanRenderOptions) {
  const surface = document.createElement('div')
  const crop = document.createElement('div')
  const frame = document.createElement('iframe')
  const badge = document.createElement('span')
  surface.style.cssText = 'display:flex;align-items:center;justify-content:center;min-width:100%;min-height:100%'
  crop.style.cssText = 'position:relative;flex-shrink:0;overflow:clip'
  frame.style.cssText = 'position:absolute;left:0;top:0;display:block;border:0;transform-origin:top left'
  frame.setAttribute('sandbox', 'allow-scripts')
  frame.setAttribute('referrerpolicy', 'no-referrer')
  frame.setAttribute('scrolling', 'no')
  badge.className = 'right-3 top-3'
  badge.style.cssText = 'position:absolute;right:12px;top:12px;z-index:10;border-radius:999px;padding:6px 12px;font:600 12px/16px system-ui,sans-serif;box-shadow:0 1px 3px #0002;pointer-events:none;white-space:nowrap'
  badge.dataset.testid = 'pelican-quality-badge'
  badge.setAttribute('role', 'status')
  crop.append(frame)
  surface.append(crop)
  const originalStyle = container.getAttribute('style')
  container.style.position = 'relative'
  container.append(surface)
  let options: PelicanRenderOptions
  let viewport = { width: 1024, height: 768 }
  let artwork = { ...viewport }
  let channel = ''
  let updates = 0

  function resize() {
    const available = { width: container.clientWidth, height: container.clientHeight }
    const fitted = fitPelicanArtwork(artwork, available, options.mode ?? 'fit')
    const innerScale = fitPelicanArtwork(artwork, viewport, 'fit').scale
    container.style.overflow = options.mode === 'actual' ? 'auto' : 'clip'
    surface.style.width = `${Math.max(available.width, fitted.width)}px`
    surface.style.height = `${Math.max(available.height, fitted.height)}px`
    crop.style.width = `${fitted.width}px`
    crop.style.height = `${fitted.height}px`
    frame.style.width = `${viewport.width}px`
    frame.style.height = `${viewport.height}px`
    frame.style.transform = `scale(${fitted.scale / innerScale})`
  }

  function update(next: PelicanRenderOptions) {
    const changed = !options || options.html !== next.html
    options = { ...next }
    if (changed) {
      const html = extractPelicanSource(options.html)
      channel = createPelicanPreviewChannel()
      updates = 0
      viewport = getPelicanViewport(html)
      artwork = { ...viewport }
      const nonce = container.ownerDocument.querySelector<HTMLScriptElement>('script[nonce]')?.nonce || ''
      frame.srcdoc = createPelicanPreviewDocument(html, channel, nonce)
    }
    frame.title = options.title || 'Pelican artwork'
    frame.style.pointerEvents = options.interactive === false ? 'none' : 'auto'
    if (options.interactive === false) frame.tabIndex = -1
    else frame.removeAttribute('tabindex')
    const quality = options.quality === undefined ? getPelicanAssessmentQuality(options.html) : options.quality
    if (!quality) badge.remove()
    if (quality) {
      if (!container.contains(badge)) container.append(badge)
      const colors = { normal: ['#d1fae5', '#065f46'], degraded: ['#fee2e2', '#991b1b'], unknown: ['#f1f5f9', '#475569'] }[quality]
      badge.style.background = colors[0]
      badge.style.color = colors[1]
      badge.dataset.quality = quality
      badge.textContent = options.badgeLabel || `满血 AI · ${{ normal: '正常', degraded: '疑似降智', unknown: '无法判定' }[quality]}`
      badge.title = options.reason || ''
    }
    resize()
  }

  function receiveSize(event: MessageEvent) {
    if (updates >= 120) return
    const size = readPelicanSizeMessage(event, frame.contentWindow, channel)
    if (!size || (size.width === artwork.width && size.height === artwork.height)) return
    updates++
    artwork = size
    resize()
  }

  update(initial)
  const observer = typeof ResizeObserver === 'undefined' ? undefined : new ResizeObserver(resize)
  observer?.observe(container)
  window.addEventListener('resize', resize)
  window.addEventListener('message', receiveSize)
  return {
    update,
    destroy() {
      observer?.disconnect()
      window.removeEventListener('resize', resize)
      window.removeEventListener('message', receiveSize)
      surface.remove()
      badge.remove()
      if (originalStyle === null) container.removeAttribute('style')
      else container.setAttribute('style', originalStyle)
    },
  }
}

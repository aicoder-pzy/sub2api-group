export interface PelicanCreativeMaterial {
  key: string
  label: string
  value: string
}

export interface PelicanCreativeRound {
  id: string
  seed: string
  pool_version: string
  started_at: string
  materials: PelicanCreativeMaterial[]
  prompt?: string
  evaluation?: PelicanCreativeEvaluation
  verification?: { quality: 'normal' | 'degraded' | 'unknown'; reason: string; checked_at: string }
  checks?: {
    has_svg: boolean
    animated: boolean
    no_external: boolean
    complete: boolean
    matched: string[]
    missing: string[]
  }
}

export interface PelicanCreativeEvaluation {
  version: string
  status: 'passed' | 'failed' | 'unknown'
  score?: number
  confidence?: 'high' | 'medium' | 'low'
  reason: string
  criteria?: { key: 'materials' | 'story' | 'visual' | 'animation'; score: number; reason: string }[]
  materials?: { key: string; status: 'present' | 'missing' | 'unclear'; reason: string }[]
  model_id?: string
  frame_times_ms?: number[]
  frame_hashes?: string[]
  source_hash?: string
  motion_detected: boolean
  checked_at: string
}

export interface PelicanCreativeMaterials {
  pool: {
    version: string
    source: string
    categories: { key: string; label: string; values: string[] }[]
  }
  round: PelicanCreativeRound
}

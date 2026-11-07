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
  checks?: {
    has_svg: boolean
    animated: boolean
    no_external: boolean
    complete: boolean
    matched: string[]
    missing: string[]
  }
}

export interface PelicanCreativeMaterials {
  pool: {
    version: string
    source: string
    categories: { key: string; label: string; values: string[] }[]
  }
  round: PelicanCreativeRound
}

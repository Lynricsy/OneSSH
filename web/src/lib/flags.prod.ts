const FLAGS = import.meta.glob<string>('/node_modules/flag-icons/flags/4x3/*.svg', {
  eager: true,
  query: '?url&no-inline',
  import: 'default',
})

export const FLAG_PREFIX = '/node_modules/flag-icons/flags/4x3/'
export const flagAsset = (key: string): string | undefined => FLAGS[key]

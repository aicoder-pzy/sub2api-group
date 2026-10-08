export default {
pelicanShowcase: {
    title: 'Pelican Showcase',
    description: 'Each group answers the same drawing prompt on a schedule. Compare model quality by looking at the results.',
    allGroups: 'All groups',
    keepRule: 'Latest {count} per group',
    retentionRule: 'Auto-removed after {days} days',
    itemCount: '{count} items',
    latestAt: 'Updated {time}',
    groupEmpty: 'No results in this group yet. They appear here once a scheduled test succeeds.',
    scrollLabel: '{group}: drag to see earlier results',
    loadError: 'Failed to load the Pelican showcase',
    itemLoading: 'Loading…',
    itemLoadError: 'Failed to load this result',
    invalidHtml: 'This result cannot be displayed',
    duration: '{seconds}s',
    reasoning: 'Reasoning {effort}',
    efforts: {
      minimal: 'minimal',
      low: 'low',
      medium: 'medium',
      high: 'high',
      xhigh: 'xhigh'
    },
    preview: 'View full size',
    previewTitle: '{group} · {model}',
    fitArtwork: 'Fit artwork',
    actualSize: '100%',
    previewSizing: 'Preview size',
    sandboxNote: 'Results run in an isolated sandbox without network access and cannot read your account.',
    remove: 'Remove from showcase',
    removeConfirm: 'Remove this result from the Pelican showcase? No user will see it any more. This cannot be undone.',
    removed: 'Removed from the showcase',
    removeFailed: 'Failed to remove',
    api: {
      title: 'API access',
      available: 'API available',
      unavailable: 'API unavailable',
      unavailableHint: 'An administrator has not enabled API Key access to these results. These examples apply once access is enabled.',
      manageKeys: 'Manage API Keys',
      readOnly: 'Free read-only access to published successful results. Requests do not run tests, call models, or deduct your API Key balance.',
      manifestEndpoint: 'Result manifest',
      itemEndpoint: 'Result content',
      copyUrl: 'Copy endpoint URL',
      authentication: 'Use a valid API Key from this site with Authorization: Bearer YOUR_API_KEY. A web login token cannot access these endpoints.',
      examples: 'Request examples',
      manifestExample: 'Read manifest',
      itemExample: 'Read content',
      cacheExample: 'Conditional request',
      copyExample: 'Copy request example',
      manifestHint: 'data.groups contains result metadata for each group. Fetch each content_url for full HTML/SVG, which is omitted from the manifest.',
      itemHint: 'Use an ID from the manifest; replace RESULT_ID if no results exist yet. data.response_text is the raw output and may contain code fences. Removed or expired results return 404.',
      cacheHint: 'Replace YOUR_ETAG with the complete ETag value from the previous response, including W/ and quotes. Keep your local manifest on 304; on 200, download only results you have not saved.',
      polling: 'Poll every 60 seconds or longer with ETag and If-None-Match. Manifest and content support GET/HEAD. Follow Retry-After on 429/503 responses.',
      keySafety: 'Keep your API Key on the calling backend instead of in public frontend code. Cross-origin browser requests should omit cookies; display artwork in an isolated iframe.'
    },
    disabled: {
      title: 'Pelican showcase is not available',
      description: 'Once an administrator enables it, scheduled results of each group appear here.'
    },
    empty: {
      title: 'Nothing to show yet',
      description: 'The administrator has not selected any groups to showcase.'
    }
  }
}

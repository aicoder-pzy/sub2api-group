export default {
  prompt: {
    title: 'Prompt policies',
    hint: 'Off by default. Match exact models after Key aliases; * is the optional fallback. Original instructions are preserved. Supports Responses, Chat Completions and Anthropic Messages. Long profiles increase input tokens and costs; evaluate results before use.',
    model: 'Exact model, or * for fallback', profile: 'Select a built-in profile', custom: 'Custom instructions', add: 'Add model policy',
    invalid: 'Enter a valid model and prompt. Each model may have only one policy.', loadFailed: 'Could not load profiles. Reopen this dialog to retry.'
  },
  sites: {
    accountName: 'Local account name',
    models: 'Discover available models', selectedModels: '{count} models selected',
    title: 'Upstream sites and key import', name: 'Site name', kind: 'Site type', url: 'Site URL', userId: 'New API user ID (optional for modern tokens)',
    token: 'Site management access token (not a model API key)', tokenHint: 'Encrypted at rest. Leave blank to retain on edit. Use a Sub2API login access token or a New API personal system token.',
    save: 'Verify and save site', select: 'Select a saved site', keys: 'Read keys and upstream groups', groups: 'Upstream group', localGroup: 'Local destination group',
    platform: 'Local account platform', concurrency: 'Account concurrency limit', rate: 'Purchase multiplier (confirm manually)', import: 'Import selected key', create: 'Create key upstream',
    createHint: 'Creation writes to the upstream immediately. After any failure refresh the key list before retrying.', keyName: 'New key name',
    saved: 'Site verified and saved', imported: 'Account imported', created: 'Creation submitted; refresh the list to confirm', empty: 'No keys', status: 'Status',
    active: 'Available', inactive: 'Unavailable', refresh: 'Refresh sites', newSite: 'Add site', delete: 'Delete site', deleteConfirm: 'Delete this site connection? Imported accounts retain their keys and configuration.',
    revision: 'Site configuration changed. Refresh and retry.', noSecret: 'Management token required', duplicate: 'This key was already imported', chooseKey: 'Select upstream key',
    proxy: 'Proxy for management requests and imported accounts', direct: 'Direct', importHint: 'Upstream keys remain on the server. Imports capture current configuration; review upstream changes and edit local accounts as needed.',
    busy: 'Working…', error: 'Operation failed. Check the site, permissions and token.', confirmed: 'I confirm creating this key upstream'
  }
}

export default {
  chat: {
    title: 'Chat', description: 'Choose a group and model to start a conversation.', newChat: 'New chat',
    conversations: 'Conversations', untitled: 'New chat', deleteChat: 'Delete conversation', deleteConfirm: 'Delete this conversation? This cannot be undone.',
    key: 'Group / API key', selectKey: 'Choose an available group', model: 'Model', selectModel: 'Choose or enter a model',
    effort: 'Reasoning effort', default: 'Model default', reload: 'Refresh', loading: 'Loading…',
    noKeys: 'No available keys. Create a key assigned to a group first.', manageKeys: 'Manage keys',
    modelsError: 'Could not load models. Refresh or enter a model name manually.', keysError: 'Could not load groups. Please refresh.',
    keyChanged: 'The key expired, ran out of quota, or changed groups. Select it again.',
    emptyTitle: 'What would you like to explore?', emptyHint: 'Ask a question, write, code, or build on an idea.',
    placeholder: 'Write a message…', send: 'Send', stop: 'Stop generating', retry: 'Regenerate',
    keyboardHint: 'Enter to send · Shift + Enter for a new line', chargingHint: 'Standard group billing applies. See Usage Records for charges.',
    historyHint: 'Conversations are saved in this browser only.', storageError: 'Could not save history locally. You can keep chatting; check your browser storage.',
    you: 'You', assistant: 'Assistant', thinking: 'Reasoning', waiting: 'Generating…', copy: 'Copy answer',
    stopped: 'Generation stopped', failed: 'Answer incomplete', limited: 'Output limit reached or content filtered',
    errors: { network: 'Connection failed. Check your connection and retry.', http: 'Request failed (HTTP {status}). Check your balance and group access, or try again later.',
      stream: 'The response did not complete. Received content has been kept.', empty: 'The model returned no text. Try another model or retry.' }
  }
}

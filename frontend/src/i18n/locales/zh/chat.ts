export default {
  chat: {
    title: '在线聊天', description: '选择分组与模型，开始对话。', newChat: '新对话',
    conversations: '历史对话', untitled: '新对话', deleteChat: '删除当前对话', deleteConfirm: '删除这段对话？删除后无法恢复。',
    key: '使用分组 / 密钥', selectKey: '选择一个可用分组', model: '模型', selectModel: '选择或输入模型名称',
    effort: '思考强度', default: '模型默认', reload: '刷新', loading: '加载中…',
    noKeys: '暂无可用密钥，请先创建一个已绑定分组的密钥。', manageKeys: '管理密钥',
    modelsError: '模型列表加载失败，可以刷新或手动输入模型名称。', keysError: '无法加载可用分组，请刷新重试。',
    keyChanged: '所选密钥已过期、额度耗尽或分组已变更，请重新选择。',
    emptyTitle: '有什么想聊的？', emptyHint: '提问、写作、编程，或者继续你的想法。',
    placeholder: '输入消息…', send: '发送', stop: '停止生成', retry: '重新生成',
    keyboardHint: 'Enter 发送 · Shift + Enter 换行', chargingHint: '按所选分组正常计费，可在使用记录中查看。',
    historyHint: '聊天记录仅保存在当前浏览器。', storageError: '聊天记录无法保存到本机，当前内容仍可使用；请检查浏览器存储空间。',
    you: '你', assistant: '助手', thinking: '思考过程', waiting: '正在生成…', copy: '复制回答',
    stopped: '已停止生成', failed: '回答未完成', limited: '回答已达到输出限制或被内容策略截断',
    errors: { network: '连接失败，请检查网络后重试。', http: '请求失败（HTTP {status}），请检查余额、分组权限或稍后重试。',
      stream: '模型未完整返回，已保留收到的内容。', empty: '模型没有返回文本回答，请更换模型或重试。' }
  }
}

export default {
  prompt: {
    title: '提示词策略',
    hint: '默认关闭。按 Key 映射后的模型精确匹配，* 表示未匹配模型的默认策略。保留原有指令；支持 Responses、Chat Completions、Anthropic Messages。长策略会增加输入 Token 和费用，实际效果需验证。',
    model: '模型名，或 * 作为默认策略', profile: '选择内置策略', custom: '自定义提示词', add: '添加模型策略',
    invalid: '请填写有效模型和提示词；同一模型只能设置一项策略。', loadFailed: '内置策略加载失败，请重新打开后重试。'
  },
  sites: {
    accountName: '本地账号名称',
    models: '拉取可用模型', selectedModels: '已选择 {count} 个模型',
    title: '上游站点与密钥导入', name: '站点名称', kind: '站点类型', url: '站点地址', userId: 'New API 用户 ID（新版令牌可留空）',
    token: '站点管理访问令牌（不是模型 API Key）', tokenHint: '令牌加密保存，编辑时留空保留。Sub2API 使用登录访问令牌；New API 使用个人设置中的系统令牌。',
    save: '验证并保存站点', select: '选择已保存站点', keys: '读取密钥与上游分组', groups: '上游分组', localGroup: '导入到本地分组',
    platform: '本地账号平台', concurrency: '账号并发上限', rate: '采购倍率（手动确认）', import: '导入选中密钥', create: '在上游创建密钥',
    createHint: '创建会立即写入上游站点。失败时先刷新密钥列表确认，避免重复创建。', keyName: '新密钥名称',
    saved: '站点验证并保存成功', imported: '账号导入成功', created: '已提交创建，请刷新列表确认', empty: '暂无密钥', status: '状态',
    active: '可用', inactive: '不可用', refresh: '刷新站点列表', newSite: '新增站点', delete: '删除站点', deleteConfirm: '删除站点连接？已导入账号保留原有密钥和配置。',
    revision: '站点配置已更改，请刷新后重试', noSecret: '缺少管理令牌', duplicate: '该密钥已导入', chooseKey: '选择上游密钥',
    proxy: '管理请求与导入账号代理', direct: '直连', importHint: '上游密钥只在服务器端读取。导入是当前配置的快照；后续上游变更请刷新确认，再编辑本地账号。',
    busy: '正在处理…', error: '操作失败，请检查站点、权限和令牌。', confirmed: '我确认在上游创建这把密钥'
  }
}

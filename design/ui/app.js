/* Meerkat interactive UI prototype — sample data only, no network, no real agents. */
'use strict';

(() => {
  // ---------------------------------------------------------------------------
  // Immutable sample fixtures
  // ---------------------------------------------------------------------------
  const deepFreeze = (o) => { Object.values(o).forEach((v) => { if (v && typeof v === 'object') deepFreeze(v); }); return Object.freeze(o); };

  const NOW = '14:32';
  const SNAPSHOT_AT = '14:29';
  const PHASES = ['任务', '开发', '初次交付', '检查', '最终候选', '精修与复验'];
  const ROLE = { dev: '开发', check: '检查', polish: '最终精修' };
  const MODELS = ['DeepSeek V4 Flash', 'Claude Opus 5.5'];

  const DATA = deepFreeze({
    projects: {
      example-project: { name: 'example-project', repo: 'example-project/web' },
      meerkat: { name: 'Meerkat', repo: 'meerkat/core' },
    },
    coordinator: { name: 'Codex', role: '需求与审查', note: '宿主会话 · 拆分任务、生成共享上下文、审查交付；不计入本地 Pi 进程' },
    agents: [
      {
        id: 'Pi-01', role: 'dev', task: 'T-104', model: 'DeepSeek V4 Flash', status: 'running', startedAt: '14:12',
        action: '修改 src/search/highlight.ts：多关键词分词与转义', actionAt: '14:29',
        cwd: '~/work/example-project-web/.worktrees/t-104', context: '共享上下文 v2 · sha256:9c1e…40ab', lastDelivery: '本任务尚无交付',
        events: [
          { t: '14:12', k: '领取', x: '领取 T-104，收到共享上下文 v2' },
          { t: '14:21', k: '读取', x: '读取 src/search/index.ts、highlight.ts、search.test.ts' },
          { t: '14:29', k: '编辑', x: '修改 highlight.ts：多关键词分词与 HTML 转义' },
        ],
      },
      {
        id: 'Pi-02', role: 'check', task: 'T-211', model: 'Claude Opus 5.5', status: 'running', startedAt: '13:58',
        action: '运行本地测试 npm test -- export', actionAt: '14:31',
        cwd: '~/work/meerkat-core/.worktrees/t-211', context: '共享上下文 v1 · sha256:51d0…e7c2', lastDelivery: '初次交付 a41c2e7（Pi-01）',
        events: [
          { t: '13:58', k: '领取', x: '收到初次交付 a41c2e7（基线 9f03b11）' },
          { t: '14:26', k: '审查', x: 'exporter.ts 字段顺序与 run-record schema 一致' },
          { t: '14:31', k: '测试', x: '运行 npm test -- export（进行中）' },
        ],
      },
      {
        id: 'Pi-03', role: 'check', task: 'T-102', model: 'DeepSeek V4 Flash', status: 'waiting', startedAt: '14:19',
        action: '排队复验 T-102 精修结果：等待并发槽位', actionAt: '14:19',
        cwd: '~/work/example-project-web/.worktrees/t-102', context: '共享上下文 v3 · sha256:5f1c…a903', lastDelivery: '精修候选 e7d5f02（Pi-01 · Claude Opus 5.5）',
        events: [
          { t: '14:10', k: '交付', x: 'Pi-01 完成最终精修，候选 e7d5f02' },
          { t: '14:19', k: '排队', x: '进入复验队列（角色：检查）' },
          { t: '14:19', k: '等待', x: '并发槽位已满（2/2），不抢占运行中任务' },
        ],
      },
    ],
    tasks: [
      {
        id: 'T-104', project: 'example-project', issue: 'APP-51', title: '搜索结果中高亮匹配关键词', stage: 1, category: 'active', owner: 'Pi-01', last: '开发中',
        issueBody: '搜索结果列表需要高亮命中的关键词，支持多个词。',
        goal: '搜索结果标题与摘要中高亮全部命中关键词。', scope: ['src/search/highlight.ts', '搜索结果组件的高亮样式'],
        acceptance: ['输入“设计 原型”时两个词都被高亮', '关键词中的 < > & 不会注入 HTML', '无匹配时文本不变'],
        blockers: '无', next: 'Pi-01 完成开发后提交初次交付，由检查角色接手。',
        ctx: { v: 'v2', digest: 'sha256:9c1e…40ab', files: ['src/search/highlight.ts', 'src/search/index.ts', 'tests/search.test.ts'], constraints: ['不引入新依赖', '高亮样式沿用现有 mark 色板'], received: ['Pi-01 · 开发 · r-104-1'] },
        delivery: null,
        timeline: [
          { t: '13:55', p: '任务', x: 'Codex 从 APP-51 拆分任务，生成上下文 v1' },
          { t: '14:08', p: '任务', x: 'Codex 补充转义约束 → 上下文 v2' },
          { t: '14:12', p: '开发', x: 'Pi-01 领取（DeepSeek V4 Flash）' },
        ],
      },
      {
        id: 'T-211', project: 'meerkat', issue: 'MKT-18', title: '运行记录导出为 JSON', stage: 3, category: 'active', owner: 'Pi-02', last: '检查中',
        issueBody: '用户希望把运行记录导出，便于自己汇总用量。',
        goal: '在本地导出单个任务的运行记录为 JSON 文件。', scope: ['src/export/exporter.ts', 'CLI 子命令 export'],
        acceptance: ['导出文件包含 run id、角色、模型、耗时、用量字段', '未返回的用量写为 null 而不是 0', '导出不含任何密钥或环境变量'],
        blockers: '无', next: 'Pi-02 完成检查；如有发现，进入一次定向修复。',
        ctx: { v: 'v1', digest: 'sha256:51d0…e7c2', files: ['src/export/exporter.ts', 'src/runs/schema.ts', 'tests/export.test.ts'], constraints: ['用量未知写 null', '不读取 .env'], received: ['Pi-01 · 开发 · r-211-1', 'Pi-02 · 检查 · r-211-2'] },
        delivery: { baseline: '9f03b11', candidate: 'a41c2e7', files: ['src/export/exporter.ts', 'src/cli/export.ts', 'tests/export.test.ts'], code: '检查进行中', tests: '检查进行中', review: '检查进行中', polish: null, preview: '示例 · 无 Preview（CLI 改动）' },
        timeline: [
          { t: '12:58', p: '任务', x: 'Codex 从 MKT-18 拆分出两个任务（T-211、T-212），生成上下文 v1' },
          { t: '13:12', p: '开发', x: 'Pi-01 领取（DeepSeek V4 Flash）' },
          { t: '13:44', p: '初次交付', x: '候选 a41c2e7，变更 3 个文件' },
          { t: '13:58', p: '检查', x: 'Pi-02 开始检查' },
        ],
      },
      {
        id: 'T-102', project: 'example-project', issue: 'APP-47', title: '订阅表单错误提示的可读性', stage: 5, category: 'active', owner: 'Pi-03（排队）', last: '精修交付 · 待复验',
        issueBody: '订阅表单出错时提示太淡，读屏软件也不会读出来。',
        goal: '订阅表单的错误提示清晰可读，并能被读屏软件读出。', scope: ['SubscribeForm 组件', '表单错误样式', '对应单元测试'],
        acceptance: ['错误文字对比度 ≥ 4.5:1（浅色与深色）', '输入框通过 aria-describedby 关联错误文字', '提交失败时焦点移到第一个出错字段'],
        blockers: '无', next: 'Pi-03 以检查角色复验精修候选 e7d5f02；通过后标记“待效果核验”，由用户确认效果。',
        ctx: {
          v: 'v3', digest: 'sha256:5f1c…a903',
          files: ['src/components/SubscribeForm.tsx', 'src/styles/form.css', 'tests/subscribe-form.test.ts'],
          constraints: ['不改动订阅接口', '文案以 v3 附带的定稿为准', '深色模式同样满足对比度'],
          received: ['Pi-01 · 开发 · r-102-1（v2）', 'Pi-02 · 检查 · r-102-2（v2）', 'Pi-01 · 定向修复 · r-102-3（v2）', 'Pi-02 · 复检 · r-102-4（v2）', 'Pi-01 · 最终精修 · r-102-5（v3）', 'Pi-03 · 复验 · r-102-6（v3，排队）'],
        },
        delivery: {
          baseline: '5b2c9d1', candidate: 'e7d5f02', reviewed: 'c3e9b42',
          files: ['src/components/SubscribeForm.tsx', 'src/styles/form.css', 'tests/subscribe-form.test.ts'],
          code: '检查发现 2 项（对比度 3.6:1；缺少 aria-describedby），定向修复 1 轮后复检通过。',
          tests: '示例：12 项单元测试通过（r-102-4 报告）。精修后待复验。',
          review: 'Codex 审查主体候选 c3e9b42：满足 v2 验收；精修交付 e7d5f02 尚待复验，尚非最终代码交付。',
          polish: { before: '错误文字与输入框之间间距 2px，文案“格式错误”。', after: '间距 6px，图标与文字对齐，文案按 v3 定稿“请输入有效的邮箱地址”。' },
          preview: '示例 · 未生成真实 Preview',
        },
        timeline: [
          { t: '09:52', p: '任务', x: 'Codex 从 APP-47 拆分任务，生成上下文 v1' },
          { t: '10:01', p: '任务', x: 'Codex 补充“读屏可读”验收 → 上下文 v2' },
          { t: '10:31', p: '初次交付', x: 'Pi-01 交付候选 8a17f30' },
          { t: '10:58', p: '检查', x: '发现 2 项：错误文字对比度 3.6:1；未关联 aria-describedby', c: 'find' },
          { t: '11:15', p: '开发', x: '定向修复（第 1/2 轮），候选 c3e9b42' },
          { t: '11:30', p: '最终候选', x: '复检通过，主体交付 c3e9b42 成为最终候选（待精修与复验）', c: 'pass' },
          { t: '13:40', p: '任务', x: 'Codex 固定文案定稿 → 上下文 v3' },
          { t: '14:10', p: '精修与复验', x: 'Pi-01 以最强配置（Claude Opus 5.5）精修，候选 e7d5f02' },
          { t: '14:19', p: '精修与复验', x: 'Pi-03 复验排队，等待并发槽位' },
        ],
      },
      {
        id: 'T-101', project: 'example-project', issue: 'APP-42', title: '文章页移动端目录折叠', stage: 4, category: 'active', owner: '—', last: '主体检查通过 · 待精修',
        issueBody: '手机上文章目录太长，挡住正文。',
        goal: '窄屏下目录默认折叠，可展开。', scope: ['ArticleToc 组件', '窄屏断点样式'],
        acceptance: ['宽度 < 600px 时目录默认折叠', '展开按钮有可读名称与 aria-expanded', '桌面布局不变'],
        blockers: '无', next: '按设置进入最终精修（最强配置），之后必须复验。',
        ctx: { v: 'v1', digest: 'sha256:a7b2…19fe', files: ['src/components/ArticleToc.tsx', 'src/styles/article.css'], constraints: ['不改桌面断点'], received: ['Pi-02 · 开发 · r-101-1', 'Pi-01 · 检查 · r-101-2'] },
        delivery: { baseline: '0c6e2b8', candidate: '2d4f9a1', reviewed: '2d4f9a1', files: ['src/components/ArticleToc.tsx', 'src/styles/article.css'], code: '首次检查即通过，无阻断发现。', tests: '示例：6 项组件测试通过（r-101-2 报告）。', review: 'Codex 审查通过，标记最终候选；精修并复验后才是最终代码交付。', polish: null, preview: '示例 · 未生成真实 Preview' },
        timeline: [
          { t: '09:05', p: '任务', x: 'Codex 从 APP-42 拆分任务，生成上下文 v1' },
          { t: '09:38', p: '初次交付', x: 'Pi-02 交付候选 2d4f9a1' },
          { t: '10:02', p: '检查', x: 'Pi-01 检查通过，无发现', c: 'pass' },
          { t: '10:04', p: '最终候选', x: '最终候选 2d4f9a1（主体检查通过，待精修与复验）', c: 'pass' },
        ],
      },
      {
        id: 'T-103', project: 'example-project', issue: 'APP-44', title: '页脚链接在深色模式下的对比度', stage: 5, done: true, category: 'attention', owner: '用户', last: '待效果核验',
        issueBody: '深色模式下页脚链接几乎看不清。',
        goal: '深色模式下页脚链接满足对比度并保持品牌色。', scope: ['页脚样式'],
        acceptance: ['深色模式链接对比度 ≥ 4.5:1', '悬停与焦点状态可辨', '浅色模式不变'],
        blockers: '无', next: '需要你在真实页面上确认效果；Meerkat 不会把它标记为已发布。',
        ctx: { v: 'v1', digest: 'sha256:0e44…c1d7', files: ['src/styles/footer.css'], constraints: ['保留品牌色相'], received: ['Pi-02 · 开发 · r-103-1', 'Pi-03 · 检查 · r-103-2', 'Pi-03 · 最终精修 · r-103-3', 'Pi-01 · 复验 · r-103-4'] },
        delivery: { baseline: '3a90f15', candidate: 'b90a3d6', reviewed: '71be0c4', final: 'b90a3d6', files: ['src/styles/footer.css'], code: '首次检查通过；精修后复验通过。', tests: '示例：样式快照 4 项通过（r-103-4 报告）。', review: 'Codex 审查通过：最终代码交付 b90a3d6（精修后复验通过；早先审查候选 71be0c4）。', polish: { before: '链接色 #7b8cff，焦点环与背景接近。', after: '链接色调整亮度保持色相，焦点环加 2px 外描边。' }, preview: '示例 · 未生成真实 Preview' },
        timeline: [
          { t: '08:12', p: '任务', x: 'Codex 从 APP-44 拆分任务，生成上下文 v1' },
          { t: '08:41', p: '初次交付', x: 'Pi-02 交付候选 71be0c4' },
          { t: '08:57', p: '检查', x: 'Pi-03 检查通过', c: 'pass' },
          { t: '08:58', p: '最终候选', x: '最终候选 71be0c4（主体检查通过，待精修与复验）', c: 'pass' },
          { t: '09:30', p: '精修与复验', x: 'Pi-03 以最强配置（Claude Opus 5.5）精修，候选 b90a3d6' },
          { t: '09:44', p: '精修与复验', x: 'Pi-01 复验通过 → 最终代码交付 b90a3d6（未发布、未经客户验收）· 待效果核验', c: 'pass' },
        ],
      },
      {
        id: 'T-212', project: 'meerkat', issue: 'MKT-18', title: '设置中并发上限的输入校验', stage: 2, category: 'active', owner: '排队（检查）', last: '初次交付',
        issueBody: '同 MKT-18：导出前希望能限制同时运行数量。',
        goal: '并发上限只能输入 1–4 的整数，并给出可读提示。', scope: ['settings/concurrency 校验'],
        acceptance: ['输入 0 或 5 时显示提示且不保存', '键盘可完成修改', '默认值仍为 2'],
        blockers: '无', next: '等待检查角色空闲后开始检查。',
        ctx: { v: 'v1', digest: 'sha256:51d0…e7c2', files: ['src/settings/concurrency.ts', 'tests/settings.test.ts'], constraints: ['默认并发 2'], received: ['Pi-03 · 开发 · r-212-1'] },
        delivery: { baseline: '9f03b11', candidate: '5e88d10', files: ['src/settings/concurrency.ts', 'tests/settings.test.ts'], code: '尚未检查', tests: '尚未检查', review: '尚未审查', polish: null, preview: '示例 · 无 Preview' },
        timeline: [
          { t: '12:58', p: '任务', x: 'Codex 从 MKT-18 拆分（与 T-211 共享 Issue，独立上下文）' },
          { t: '13:05', p: '初次交付', x: 'Pi-03 交付候选 5e88d10' },
        ],
      },
      {
        id: 'T-213', project: 'meerkat', issue: 'MKT-22', title: '断连后将快照标记为过期', stage: 0, category: 'active', owner: '未分派', last: '待分派',
        issueBody: '连接断开时界面仍显示旧数字，看起来像实时数据。',
        goal: '断连时界面显示“运行数未知”，并标记最近快照为过期。', scope: ['状态订阅', '摘要文案'],
        acceptance: ['断连后 5 秒内显示“未知”', '不自动无限重连'], blockers: '无', next: '等待并发槽位后分派开发角色。',
        ctx: { v: 'v1', digest: 'sha256:d3f8…02b6', files: ['src/state/subscribe.ts'], constraints: ['重连最多 2 次后停止'], received: [] },
        delivery: null,
        timeline: [{ t: '14:02', p: '任务', x: 'Codex 从 MKT-22 拆分任务，生成上下文 v1' }],
      },
    ],
    // tokens: in/out/cr(cacheRead)/cw(cacheWrite); null = not returned. fee: sample provider-reported amount, null = unknown.
    runs: [
      { id: 'r-103-1', task: 'T-103', agent: 'Pi-02', role: 'dev', model: 'DeepSeek V4 Flash', start: '08:20', end: '08:41', result: '初次交付 71be0c4', tokens: { in: 21400, out: 2900, cr: 14800, cw: 4100 }, fee: 0.22 },
      { id: 'r-103-2', task: 'T-103', agent: 'Pi-03', role: 'check', model: 'DeepSeek V4 Flash', start: '08:44', end: '08:57', result: '通过', first: 'pass', tokens: { in: 9800, out: 1100, cr: 7200, cw: 1600 }, fee: 0.08 },
      { id: 'r-103-3', task: 'T-103', agent: 'Pi-03', role: 'polish', model: 'Claude Opus 5.5', start: '09:02', end: '09:30', result: '精修候选 b90a3d6', tokens: null, fee: null },
      { id: 'r-103-4', task: 'T-103', agent: 'Pi-01', role: 'check', model: 'DeepSeek V4 Flash', start: '09:33', end: '09:44', result: '复验通过 → 最终代码交付 b90a3d6', tokens: { in: 8700, out: 900, cr: 6900, cw: 1200 }, fee: 0.07 },
      { id: 'r-101-1', task: 'T-101', agent: 'Pi-02', role: 'dev', model: 'DeepSeek V4 Flash', start: '09:10', end: '09:38', result: '初次交付 2d4f9a1', tokens: { in: 30100, out: 4200, cr: 19400, cw: 6200 }, fee: 0.31 },
      { id: 'r-101-2', task: 'T-101', agent: 'Pi-01', role: 'check', model: 'DeepSeek V4 Flash', start: '09:45', end: '10:02', result: '通过', first: 'pass', tokens: { in: 12500, out: 1300, cr: 9100, cw: 2000 }, fee: 0.12 },
      { id: 'r-102-1', task: 'T-102', agent: 'Pi-01', role: 'dev', model: 'DeepSeek V4 Flash', start: '10:05', end: '10:31', result: '初次交付 8a17f30', tokens: { in: 48200, out: 6100, cr: 31000, cw: 9800 }, fee: 0.38 },
      { id: 'r-102-2', task: 'T-102', agent: 'Pi-02', role: 'check', model: 'DeepSeek V4 Flash', start: '10:36', end: '10:58', result: '发现 2 项', first: 'fail', tokens: { in: 15600, out: 1900, cr: 11200, cw: 2400 }, fee: 0.14 },
      { id: 'r-102-3', task: 'T-102', agent: 'Pi-01', role: 'dev', fix: true, model: 'DeepSeek V4 Flash', start: '11:02', end: '11:15', result: '定向修复 c3e9b42', tokens: { in: 11900, out: 1700, cr: 9300, cw: null }, fee: null },
      { id: 'r-102-4', task: 'T-102', agent: 'Pi-02', role: 'check', model: 'DeepSeek V4 Flash', start: '11:18', end: '11:30', result: '复检通过 → 最终候选', tokens: { in: 10400, out: 1000, cr: 8100, cw: 1500 }, fee: 0.09 },
      { id: 'r-212-1', task: 'T-212', agent: 'Pi-03', role: 'dev', model: 'DeepSeek V4 Flash', start: '12:30', end: '13:05', result: '初次交付 5e88d10', tokens: { in: 26300, out: 3800, cr: null, cw: null }, fee: null },
      { id: 'r-211-1', task: 'T-211', agent: 'Pi-01', role: 'dev', model: 'DeepSeek V4 Flash', start: '13:12', end: '13:44', result: '初次交付 a41c2e7', tokens: { in: 33800, out: 4600, cr: 22500, cw: 7000 }, fee: 0.29 },
      { id: 'r-102-5', task: 'T-102', agent: 'Pi-01', role: 'polish', model: 'Claude Opus 5.5', start: '13:48', end: '14:10', result: '精修候选 e7d5f02', tokens: { in: 61200, out: 9400, cr: null, cw: null }, fee: null },
      { id: 'r-211-2', task: 'T-211', agent: 'Pi-02', role: 'check', model: 'Claude Opus 5.5', start: '13:58', end: null, result: '运行中', tokens: null, fee: null },
      { id: 'r-104-1', task: 'T-104', agent: 'Pi-01', role: 'dev', model: 'DeepSeek V4 Flash', start: '14:12', end: null, result: '运行中', tokens: null, fee: null },
      { id: 'r-102-6', task: 'T-102', agent: 'Pi-03', role: 'check', model: 'DeepSeek V4 Flash', start: null, end: null, result: '排队', tokens: null, fee: null },
    ],
    deliveries: [
      { task: 'T-102', at: '14:10', change: '错误提示间距与文案按定稿精修', agent: 'Pi-01', model: 'Claude Opus 5.5', result: '精修交付 · 待复验' },
      { task: 'T-211', at: '13:44', change: '新增 export 子命令，未知用量写 null', agent: 'Pi-01', model: 'DeepSeek V4 Flash', result: '检查中' },
      { task: 'T-212', at: '13:05', change: '并发上限限制为 1–4 并显示提示', agent: 'Pi-03', model: 'DeepSeek V4 Flash', result: '初次交付' },
      { task: 'T-101', at: '10:04', change: '窄屏目录默认折叠，补充 aria-expanded', agent: 'Pi-02', model: 'DeepSeek V4 Flash', result: '主体检查通过 · 待精修' },
      { task: 'T-103', at: '09:44', change: '深色模式链接对比度精修，复验通过，最终 SHA b90a3d6', agent: 'Pi-03', model: 'Claude Opus 5.5', result: '最终代码交付 · 待效果核验' },
    ],
  });

  const BLOCKED = deepFreeze({
    task: 'T-104',
    reason: 'APP-51 未说明高亮是否区分大小写，两种实现会改变现有搜索测试的预期。',
    next: 'Codex 在 Issue 中确认规则 → 生成上下文 v3 → 重新分派 Pi-01（第 2/2 轮）。',
    rounds: '已用 1/2 轮 · 不会自动重试',
  });

  // ---------------------------------------------------------------------------
  // Mutable UI state (prototype only)
  // ---------------------------------------------------------------------------
  const state = {
    view: 'agents', project: 'all', demo: 'normal', expanded: new Set(),
    query: '', filter: 'all', drawer: null, drawerTab: 'overview', issueOpen: false, returnFocus: null,
    sim: {},
    settings: { profiles: { dev: 'DeepSeek V4 Flash', check: 'Claude Opus 5.5', polish: 'Claude Opus 5.5' }, concurrency: 2, maxRounds: 2, tokenBudget: 200000, minuteBudget: 45 },
  };

  // ---------------------------------------------------------------------------
  // Helpers
  // ---------------------------------------------------------------------------
  const $ = (s, r = document) => r.querySelector(s);
  const esc = (s) => String(s).replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
  const toMin = (hm) => { const [h, m] = hm.split(':').map(Number); return h * 60 + m; };
  const dur = (m) => (m >= 60 ? `${Math.floor(m / 60)} 小时 ${m % 60} 分` : `${m} 分`);
  const num = (n) => n.toLocaleString('en-US');
  const UNK = '<em class="unknown">未知 / 未返回</em>';
  const taskById = (id) => DATA.tasks.find((t) => t.id === id);
  const inProject = (projectId) => state.project === 'all' || state.project === projectId;
  const projName = (id) => DATA.projects[id].name;
  const runMinutes = (r) => (r.start ? toMin(r.end || NOW) - toMin(r.start) : 0);
  const completeness = (r) => {
    if (!r.tokens) return 'none';
    return Object.values(r.tokens).every((v) => v !== null) ? 'full' : 'partial';
  };
  const resultClass = (res) => ({ 初次交付: 'accent', 检查中: 'amber', '最终代码交付 · 待效果核验': 'green', '精修交付 · 待复验': 'amber', '主体检查通过 · 待精修': 'accent', 待效果核验: 'amber', 开发中: '', 待分派: '', 已阻塞: 'red' }[res] || '');

  function taskView(t) {
    if (state.demo === 'blocked' && t.id === BLOCKED.task) {
      return { ...t, category: 'attention', last: '已阻塞', blockers: BLOCKED.reason, next: BLOCKED.next, blocked: true };
    }
    return t;
  }
  const tasks = () => DATA.tasks.map(taskView);

  function agentsView() {
    if (state.demo === 'empty') return [];
    return DATA.agents.map((a) => {
      if (state.demo === 'blocked' && a.task === BLOCKED.task) {
        return { ...a, status: 'blocked', action: '已停止：等待需求澄清（不自动重试）', actionAt: '14:30' };
      }
      if (state.sim[a.id] === 'stopped') return { ...a, status: 'waiting', action: '模拟：已标记暂停（未向任何进程发送信号）', actionAt: NOW };
      return a;
    }).filter((a) => inProject(taskById(a.task).project));
  }

  let toastTimer = 0;
  function toast(msg) {
    const el = $('#toast');
    el.textContent = msg;
    el.classList.add('show');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => el.classList.remove('show'), 2600);
  }

  // ---------------------------------------------------------------------------
  // Header summary
  // ---------------------------------------------------------------------------
  function renderSummary() {
    const el = $('#state-sum');
    if (state.demo === 'disconnected') {
      el.innerHTML = `运行数 <b>未知</b> · 最近快照 ${SNAPSHOT_AT} 已过期`;
      return;
    }
    const ag = agentsView();
    const running = ag.filter((a) => a.status === 'running').length;
    const waiting = ag.filter((a) => a.status === 'waiting').length;
    const blocked = ag.filter((a) => a.status === 'blocked').length;
    el.innerHTML = `<b>${running}</b> 运行 · <b>${waiting}</b> 等待${blocked ? ` · <b>${blocked}</b> 阻塞` : ''} · 并发上限 ${state.settings.concurrency}`;
  }

  // ---------------------------------------------------------------------------
  // Agents view
  // ---------------------------------------------------------------------------
  function statusText(a) {
    return { running: '运行中', waiting: '等待', blocked: '已阻塞', stale: '快照' }[a.status];
  }

  function agentRow(a, stale) {
    const t = taskView(taskById(a.task));
    const open = state.expanded.has(a.id);
    const elapsed = dur(toMin(NOW) - toMin(a.startedAt));
    const nextModel = state.settings.profiles[a.role];
    const nextNote = nextModel !== a.model ? `<dt>下次运行</dt><dd>${esc(ROLE[a.role])}配置当前设置为 ${esc(nextModel)}（本次运行仍使用 ${esc(a.model)}）</dd>` : '';
    const dot = stale ? 'stale' : a.status;
    return `
    <div class="agent-row${stale ? ' stale' : ''}">
      <button type="button" class="row-btn" data-agent="${a.id}" aria-expanded="${open}" aria-controls="more-${a.id}">
        <span class="dot ${dot}" title="${esc(stale ? '快照（已过期）' : statusText(a))}"></span>
        <span class="who">
          <span class="line1"><span class="role">${a.id} · ${ROLE[a.role]}</span><span class="task">${esc(t.title)}</span></span>
          <span class="sub">${esc(projName(t.project))} · ${esc(t.issue)} · <span class="act">${esc(a.action)}</span> · ${a.actionAt}${stale ? ' · 快照' : ''}</span>
        </span>
        <span class="fields">
          <span class="field"><span class="v">${esc(a.model)}</span></span>
          <span class="field"><span class="v">${a.status === 'blocked' ? '已阻塞' : esc(PHASES[t.stage] === '精修与复验' ? '复验' : PHASES[t.stage])}</span></span>
          <span class="field f-elapsed" title="${a.status === 'waiting' ? '已等待' : '已用时'}"><span class="k">${a.status === 'waiting' ? '等待' : '用时'}</span><span class="v">${elapsed}</span></span>
        </span>
        <svg class="chev-r" width="10" height="10" viewBox="0 0 10 10" aria-hidden="true"><path d="M3.5 2 6.5 5 3.5 8" fill="none" stroke="currentColor" stroke-width="1.4"/></svg>
      </button>
      <div class="agent-more" id="more-${a.id}" ${open ? '' : 'hidden'}>
        <div>
          <h4>最近 3 条结构化事件${stale ? '（快照）' : ''}</h4>
          <ul class="events">${a.events.map((e) => `<li><time>${e.t}</time><span class="ek">${esc(e.k)}</span><span>${esc(e.x)}</span></li>`).join('')}</ul>
          ${a.status === 'blocked' ? `<p class="local-note mt">阻塞原因：${esc(BLOCKED.reason)}</p>` : ''}
        </div>
        <div>
          <h4>上下文</h4>
          <dl class="kv">
            <dt>工作目录</dt><dd><code>${esc(a.cwd)}</code></dd>
            <dt>仓库</dt><dd><code>${esc(DATA.projects[t.project].repo)}</code></dd>
            <dt>上下文</dt><dd>${esc(a.context)}</dd>
            <dt>最近交付</dt><dd>${esc(a.lastDelivery)}</dd>
            ${nextNote}
          </dl>
          <div class="inline-actions">
            <button type="button" class="btn" data-open-task="${t.id}">打开任务</button>
            ${stale ? '' : `<button type="button" class="btn sim" data-sim-stop="${a.id}">${state.sim[a.id] === 'stopped' ? '恢复显示' : '停止'}</button>`}
          </div>
        </div>
      </div>
    </div>`;
  }

  function renderAgents() {
    const ag = agentsView();
    const stale = state.demo === 'disconnected';
    const c = DATA.coordinator;
    let banner = '';
    if (stale) {
      banner = `<div class="stale-banner" role="status"><span><b>已断连</b> · 运行数未知。下方为 ${SNAPSHOT_AT} 的最近快照，已过期，不代表当前状态。不会自动无限重连。</span><button type="button" class="btn sim" data-sim-reconnect>重新连接</button></div>`;
    } else if (state.demo === 'blocked' && inProject(taskById(BLOCKED.task).project)) {
      banner = `<div class="block-banner" role="status"><span><b>${BLOCKED.task} 已阻塞</b> · ${esc(BLOCKED.reason)}<br>下一步：${esc(BLOCKED.next)}<br><span class="k">${esc(BLOCKED.rounds)}</span></span></div>`;
    }
    const running = ag.filter((a) => a.status === 'running').length;
    const waiting = ag.filter((a) => a.status === 'waiting').length;
    const headCount = stale ? '运行数未知' : `${running} 运行 / ${waiting} 等待`;
    const list = ag.length
      ? `<div class="list">${ag.map((a) => agentRow(a, stale)).join('')}</div>`
      : `<div class="list"><div class="empty">0 个运行中的 Pi 实例。<br>新任务分派后会出现在这里；当前没有排队进程。</div></div>`;

    const dl = DATA.deliveries.filter((d) => inProject(taskById(d.task).project)).slice(0, 5);
    const dRows = dl.length ? dl.map((d) => {
      const t = taskById(d.task);
      return `<button type="button" class="row deliv-row" data-open-task="${t.id}">
        <span class="who"><span class="name">${esc(t.title)}</span>
        <span class="sub">${esc(t.issue)} · ${esc(d.change)} · ${d.agent} · ${esc(d.model)} · ${d.at}</span></span>
        <span class="badge res ${resultClass(d.result)}">${d.result}</span>
      </button>`;
    }).join('') : '<div class="empty">该项目暂无交付。</div>';

    $('#view-agents').innerHTML = `
      ${banner}
      <div class="host"><span class="badge accent">宿主</span><span><b>${c.name} · ${c.role}</b> — ${esc(c.note)}</span></div>
      <div class="sec-h"><b>本地 Pi 实例</b><span>${headCount}</span><span class="end">${stale ? `快照 ${SNAPSHOT_AT}` : `更新于 ${NOW}`}</span></div>
      ${list}
      <div class="sec-h"><b>最近交付</b><span>最终代码交付需经精修与复验，仍不代表已发布或客户验收</span></div>
      <div class="list">${dRows}</div>`;
  }

  // ---------------------------------------------------------------------------
  // Tasks view
  // ---------------------------------------------------------------------------
  const FILTERS = [['all', '全部'], ['active', '进行中'], ['delivered', '已交付'], ['attention', '需处理']];

  function renderTasks() {
    const scoped = tasks().filter((t) => inProject(t.project));
    const q = state.query.trim().toLowerCase();
    const matched = scoped.filter((t) => !q || [t.title, t.issue, t.id, t.owner, t.last, projName(t.project)].join(' ').toLowerCase().includes(q));
    const inFilter = (t, f) => f === 'all' || (f === 'delivered' ? !!t.done : t.category === f);
    const shown = matched.filter((t) => inFilter(t, state.filter));
    const count = (f) => matched.filter((t) => inFilter(t, f)).length;
    const rows = shown.length ? shown.map((t) => `
      <button type="button" class="row task-row" data-open-task="${t.id}">
        <span class="who"><span class="name">${esc(t.title)}</span>
        <span class="sub">${t.id} · ${esc(projName(t.project))} · ${esc(t.issue)}（示例 Issue）</span></span>
        <span class="meta"><span class="badge">${t.done ? '待效果核验' : PHASES[t.stage]}</span><span class="badge ${resultClass(t.last)}">${esc(t.last)}</span><span class="owner">${esc(t.owner)}</span></span>
      </button>`).join('') : '<div class="empty">没有匹配的任务。</div>';
    const sec = $('#view-tasks');
    const hadFocus = document.activeElement && document.activeElement.id === 'task-search';
    sec.innerHTML = `
      <div class="toolbar">
        <label class="sr-only" for="task-search">搜索任务</label>
        <input id="task-search" class="search" type="search" placeholder="搜索标题、Issue、负责人…" value="${esc(state.query)}" autocomplete="off">
        <div class="mini-seg" role="group" aria-label="任务筛选">
          ${FILTERS.map(([k, l]) => `<button type="button" data-filter="${k}" aria-pressed="${state.filter === k}">${l}<span class="n">${count(k)}</span></button>`).join('')}
        </div>
      </div>
      <div class="list">${rows}</div>`;
    if (hadFocus) { const i = $('#task-search'); i.focus(); i.setSelectionRange(i.value.length, i.value.length); }
  }

  // ---------------------------------------------------------------------------
  // Usage view
  // ---------------------------------------------------------------------------
  function sumTokens(runs) {
    const s = { in: 0, out: 0, cr: 0, cw: 0, crN: 0, cwN: 0, reported: 0 };
    runs.forEach((r) => {
      if (!r.tokens) return;
      s.reported += 1;
      if (r.tokens.in !== null) s.in += r.tokens.in;
      if (r.tokens.out !== null) s.out += r.tokens.out;
      if (r.tokens.cr !== null) { s.cr += r.tokens.cr; s.crN += 1; }
      if (r.tokens.cw !== null) { s.cw += r.tokens.cw; s.cwN += 1; }
    });
    return s;
  }

  function renderUsage() {
    const runs = DATA.runs.filter((r) => inProject(taskById(r.task).project) && r.start);
    const sec = $('#view-usage');
    if (!runs.length) { sec.innerHTML = '<div class="list"><div class="empty">该项目暂无运行记录。</div></div>'; return; }
    const full = runs.filter((r) => completeness(r) === 'full').length;
    const s = sumTokens(runs);
    const feeRuns = runs.filter((r) => r.fee !== null);
    const fee = feeRuns.reduce((a, r) => a + r.fee, 0);
    const wall = Math.max(...runs.map((r) => toMin(r.end || NOW))) - Math.min(...runs.map((r) => toMin(r.start)));
    const agentSum = runs.reduce((a, r) => a + runMinutes(r), 0);
    const mixed = full < runs.length;

    // efficiency examples — computed from fixtures, denominators shown
    const firstChecks = runs.filter((r) => r.first);
    const firstPass = firstChecks.filter((r) => r.first === 'pass').length;
    const fixes = runs.filter((r) => r.fix).length;
    const checkedTasks = new Set(firstChecks.map((r) => r.task)).size;
    const reviewMin = runs.filter((r) => r.role === 'check').reduce((a, r) => a + runMinutes(r), 0);

    const groups = {};
    runs.forEach((r) => { const k = `${r.role}|${r.model}`; (groups[k] = groups[k] || []).push(r); });

    const groupRows = Object.entries(groups).map(([k, rs]) => {
      const [role, model] = k.split('|');
      const gs = sumTokens(rs);
      const gFee = rs.filter((r) => r.fee !== null);
      const gFull = rs.filter((r) => completeness(r) === 'full').length;
      const tok = gs.reported ? `${gFull < rs.length ? '≥ ' : ''}${num(gs.in + gs.out)}` : '未知';
      return `<div class="row stat-row">
        <span class="who"><span class="name">${ROLE[role]} · ${esc(model)}</span>
        <span class="sub">${rs.length} 次运行 · 用量完整 ${gFull}/${rs.length} · 耗时合计 ${dur(rs.reduce((a, r) => a + runMinutes(r), 0))}</span></span>
        <span class="num"><b>${tok}</b><small>tokens（已上报）</small></span>
        <span class="num"><b>${gs.crN ? num(gs.cr) : '未知'} / ${gs.cwN ? num(gs.cw) : '未知'}</b><small>缓存 读 / 写</small></span>
        <span class="num"><b>${gFee.length ? `¥${gFee.reduce((a, r) => a + r.fee, 0).toFixed(2)}` : '未知'}</b><small>${gFee.length}/${rs.length} 次返回费用</small></span>
      </div>`;
    }).join('');

    const runRows = runs.map((r) => {
      const c = completeness(r);
      const tk = r.tokens;
      const tokTxt = tk ? `入 ${tk.in === null ? '未返回' : num(tk.in)} · 出 ${tk.out === null ? '未返回' : num(tk.out)} · 缓存读 ${tk.cr === null ? '未返回' : num(tk.cr)} · 缓存写 ${tk.cw === null ? '未返回' : num(tk.cw)}` : (r.end ? '用量未返回' : '运行中，尚未返回用量');
      return `<div class="row stat-row">
        <span class="who"><span class="name"><code>${r.id}</code> · ${r.agent} · ${ROLE[r.role]}${r.fix ? '（定向修复）' : ''}</span>
        <span class="sub">${esc(r.model)} · ${esc(tokTxt)}</span></span>
        <span class="num"><b>${dur(runMinutes(r))}${r.end ? '' : '+'}</b><small>${r.start}–${r.end || '进行中'}</small></span>
        <span class="num"><b>${r.fee !== null ? `¥${r.fee.toFixed(2)}` : '未知'}</b><small>${{ full: '用量完整', partial: '用量部分', none: '用量未返回' }[c]}</small></span>
      </div>`;
    }).join('');

    sec.innerHTML = `
      <div class="sec-h"><b>用量</b><span class="example-tag">示例数据</span><span>非真实账单；数字来自原型样例</span></div>
      <div class="stats">
        <div class="kpi"><b>${runs.length}</b><span>运行次数</span><small>用量完整 ${full}/${runs.length}</small></div>
        <div class="kpi"><b>${mixed ? '≥ ' : ''}${num(s.in + s.out)}</b><span>已上报 tokens</span><small>${s.reported}/${runs.length} 次有上报${mixed ? '，非完整总量' : ''}</small></div>
        <div class="kpi"><b>${num(s.cr)} / ${num(s.cw)}</b><span>缓存 读 / 写</span><small>上报 ${s.crN} / ${s.cwN} 次</small></div>
        <div class="kpi"><b>¥${fee.toFixed(2)}</b><span>已返回费用（示例）</span><small>${runs.length - feeRuns.length} 次未知，不估算</small></div>
        <div class="kpi"><b>${dur(wall)}</b><span>墙钟时间</span><small>Agent 耗时合计 ${dur(agentSum)}</small></div>
      </div>

      <div class="sec-h"><b>按角色 / 模型</b><span>“≥” 表示有运行未返回用量</span></div>
      <div class="list">${groupRows}</div>

      <div class="sec-h"><b>流程指标</b><span class="example-tag">示例</span></div>
      <div class="list">
        <div class="row stat-row"><span class="who"><span class="name">初次交付一次通过</span><span class="sub">首次检查未发现问题的任务 / 已完成首次检查的任务</span></span><span class="num"><b>${checkedTasks ? `${firstPass} / ${checkedTasks}` : '—'}</b><small>任务</small></span></div>
        <div class="row stat-row"><span class="who"><span class="name">定向修复次数</span><span class="sub">修复运行数 / 已检查任务数（上限 ${state.settings.maxRounds} 轮）</span></span><span class="num"><b>${fixes} / ${checkedTasks}</b><small>次 / 任务</small></span></div>
        <div class="row stat-row"><span class="who"><span class="name">检查耗时占比</span><span class="sub">检查与复验运行耗时 / Agent 耗时合计</span></span><span class="num"><b>${dur(reviewMin)} / ${dur(agentSum)}</b><small>${agentSum ? Math.round((reviewMin / agentSum) * 100) : 0}%</small></span></div>
      </div>

      <div class="sec-h"><b>逐次运行</b><span>${runs.length} 次 · 排队中的运行不计入</span></div>
      <div class="list">${runRows}</div>`;
  }

  // ---------------------------------------------------------------------------
  // Task detail drawer
  // ---------------------------------------------------------------------------
  const TABS = [['overview', '概览'], ['context', '共享上下文'], ['delivery', '交付与检查'], ['runs', '运行记录']];

  function drawerBody(t) {
    const runs = DATA.runs.filter((r) => r.task === t.id);
    if (state.drawerTab === 'overview') {
      return `
        <div class="card"><h3>目标</h3><p>${esc(t.goal)}</p></div>
        <div class="card"><h3>范围</h3><ul>${t.scope.map((x) => `<li>${esc(x)}</li>`).join('')}</ul></div>
        <div class="card"><h3>可观察的验收标准</h3><ul>${t.acceptance.map((x) => `<li>${esc(x)}</li>`).join('')}</ul></div>
        <div class="card"><h3>阻塞与下一步</h3><dl class="concl"><dt>阻塞</dt><dd>${esc(t.blockers)}</dd><dt>下一步</dt><dd>${esc(t.next)}</dd>${t.blocked ? `<dt>重试</dt><dd>${esc(BLOCKED.rounds)}</dd>` : ''}</dl></div>
        <div class="card"><h3>时间线</h3><ol class="timeline">${t.timeline.map((e) => `<li class="${e.c || ''}"><time>${e.t}</time><span class="ph">${esc(e.p)}</span><span class="tx">${esc(e.x)}</span></li>`).join('')}${t.blocked ? `<li class="find"><time>14:30</time><span class="ph">开发</span><span class="tx">已阻塞：${esc(BLOCKED.reason)}</span></li>` : ''}</ol></div>`;
    }
    if (state.drawerTab === 'context') {
      const c = t.ctx;
      const nextV = `v${Number(c.v.slice(1)) + 1}`;
      return `
        <div class="card"><h3>共享上下文 ${c.v} · 不可变</h3>
          <dl class="concl"><dt>版本</dt><dd>${c.v}（已批准，只读）</dd><dt>来源摘要</dt><dd><code>${esc(c.digest)}</code> · 由 ${esc(t.issue)}（示例 Issue）与 Codex 审定的需求生成</dd></dl></div>
        <div class="card"><h3>相关文件</h3><ul class="files">${c.files.map((f) => `<li>${esc(f)}</li>`).join('')}</ul></div>
        <div class="card"><h3>约束</h3><ul>${c.constraints.map((x) => `<li>${esc(x)}</li>`).join('')}</ul></div>
        <div class="card"><h3>运行使用的上下文版本</h3>${c.received.length ? `<ul>${c.received.map((x) => `<li>${esc(x)}</li>`).join('')}</ul>` : '<p class="k">尚无运行接收。</p>'}</div>
        <div class="card"><h3>如何变成 ${nextV}</h3><p>需求或约束变化时，由 Codex 生成新的 ${nextV}（新摘要）并重新分派；${c.v} 不会被就地修改，已完成的运行继续引用 ${c.v}。各运行只拿到共享包与本任务文件，不复制完整对话记录。</p></div>`;
    }
    if (state.drawerTab === 'delivery') {
      const d = t.delivery;
      if (!d) return '<div class="card"><p class="k">尚无交付。开发完成后会在此显示基线、候选与检查结论。</p></div>';
      return `
        <div class="card"><h3>版本</h3><dl class="concl">
          <dt>基线</dt><dd><code>${d.baseline}</code></dd>
          <dt>候选</dt><dd><code>${d.candidate}</code></dd>
          ${d.reviewed ? `<dt>主体候选</dt><dd><code>${d.reviewed}</code> · 检查通过${d.final ? '（早先审查版本）' : ''}</dd>` : ''}
          ${d.final ? `<dt>最终代码</dt><dd><code>${d.final}</code> · 精修后复验通过</dd>` : ''}
          <dt>仓库</dt><dd><code>${esc(DATA.projects[t.project].repo)}</code></dd></dl></div>
        <div class="card"><h3>变更文件（${d.files.length}）</h3><ul class="files">${d.files.map((f) => `<li>${esc(f)}</li>`).join('')}</ul></div>
        <div class="card"><h3>结论</h3><dl class="concl"><dt>代码检查</dt><dd>${esc(d.code)}</dd><dt>本地测试</dt><dd>${esc(d.tests)}</dd><dt>审查</dt><dd>${esc(d.review)}</dd></dl>
          <p class="k mt small">结论已汇总，无需你逐行审阅 diff。</p></div>
        ${d.polish ? `<div class="card"><h3>精修前 / 后</h3><dl class="concl"><dt>精修前</dt><dd>${esc(d.polish.before)}</dd><dt>精修后</dt><dd>${esc(d.polish.after)}</dd></dl></div>` : ''}
        <div class="card"><h3>Preview</h3><p><span class="badge amber">${esc(d.preview)}</span></p></div>`;
    }
    // runs
    if (!runs.length) return '<div class="card"><p class="k">尚无运行记录。</p></div>';
    return `<div class="card"><div class="run-table">${runs.map((r) => {
      const tk = r.tokens;
      const c = completeness(r);
      const tokens = tk ? `${tk.in === null || tk.out === null ? '' : num(tk.in + tk.out)} tokens · 缓存 ${tk.cr === null ? '未返回' : num(tk.cr)} / ${tk.cw === null ? '未返回' : num(tk.cw)}` : null;
      return `<div class="run">
        <span><span class="rid">${r.id}</span> · <span class="rt">${r.agent} · ${ROLE[r.role]}${r.fix ? '（定向修复）' : ''}</span></span>
        <span class="rr">${esc(r.result)}</span>
        <span class="rm">${esc(r.model)} · ${r.start ? `${dur(runMinutes(r))}${r.end ? '' : '+'}` : '未开始'} · 用量 ${tokens ? esc(tokens) : UNK}${c === 'partial' ? '（部分）' : ''} · 费用 ${r.fee !== null ? `¥${r.fee.toFixed(2)}（示例）` : UNK}</span>
      </div>`;
    }).join('')}</div></div>`;
  }

  function renderDrawer() {
    const t = taskView(taskById(state.drawer));
    const p = t.stage;
    const phases = PHASES.map((name, i) => {
      let cls = i < p || (t.done && i === p) ? 'done' : (i === p ? 'cur' : '');
      if (t.blocked && i === p) cls = 'block';
      return `<li class="${cls}"${i === p ? ' aria-current="step"' : ''}>${name}</li>`;
    }).join('');
    const finalState = t.done ? '<span class="badge amber">待效果核验</span><span>精修已复验通过，等待你确认真实效果</span>'
      : t.stage === 5 ? '<span class="badge amber">精修交付 · 待复验</span><span>最强配置精修后必须再经检查，才进入“待效果核验”</span>'
        : t.blocked ? `<span class="badge red">已阻塞</span><span>${esc(BLOCKED.rounds)}</span>`
          : `<span class="badge">${PHASES[t.stage]}</span><span>${esc(t.last)}</span>`;
    $('#drawer').innerHTML = `
      <div class="dlg-h">
        <div>
          <h2 id="drawer-title">${esc(t.title)}</h2>
          <div class="sub"><span>${t.id} · ${esc(projName(t.project))}</span>
            <button type="button" class="issue-chip" id="issue-btn" aria-expanded="${state.issueOpen}" aria-controls="issue-prev">${esc(t.issue)} · 示例 Issue</button></div>
        </div>
        <button type="button" class="icon" id="drawer-close" aria-label="关闭任务详情"><svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5"/></svg></button>
      </div>
      <div class="dlg-b">
        <div class="issue-prev" id="issue-prev" ${state.issueOpen ? '' : 'hidden'}><b>${esc(t.issue)}</b>：${esc(t.issueBody)}<span class="local-note">示例来源：此 Issue 为原型虚构，不链接任何真实页面。</span></div>
        <ol class="phases" aria-label="阶段">${phases}</ol>
        <div class="final-state">${finalState}</div>
        <div class="mini-seg tabs" role="tablist" aria-label="任务详情分区">
          ${TABS.map(([k, l]) => `<button type="button" role="tab" id="tab-${k}" data-tab="${k}" aria-selected="${state.drawerTab === k}" aria-controls="tabpanel" tabindex="${state.drawerTab === k ? 0 : -1}">${l}</button>`).join('')}
        </div>
        <div id="tabpanel" role="tabpanel" aria-labelledby="tab-${state.drawerTab}">${drawerBody(t)}</div>
      </div>`;
  }

  function openTask(id, opener) {
    state.drawer = id;
    state.drawerTab = 'overview';
    state.issueOpen = false;
    state.returnFocus = opener || document.activeElement;
    renderDrawer();
    $('#drawer-scrim').hidden = false;
    $('#drawer-close').focus();
  }

  function closeDialog(which) {
    const scrim = which === 'drawer' ? $('#drawer-scrim') : $('#settings-scrim');
    if (scrim.hidden) return;
    scrim.hidden = true;
    if (which === 'drawer') state.drawer = null;
    const back = state.returnFocus;
    state.returnFocus = null;
    // the opener may have been re-rendered; fall back to an equivalent element
    if (back && document.contains(back)) back.focus();
    else if (back && back.dataset && back.dataset.openTask) { const alt = document.querySelector(`[data-open-task="${back.dataset.openTask}"]`); if (alt) alt.focus(); }
  }

  // ---------------------------------------------------------------------------
  // Settings sheet
  // ---------------------------------------------------------------------------
  function renderSettings() {
    const s = state.settings;
    const opt = (list, v) => list.map((x) => `<option${x === v ? ' selected' : ''}>${esc(x)}</option>`).join('');
    $('#settings').innerHTML = `
      <div class="dlg-h"><div><h2 id="settings-title">设置</h2><div class="sub">仅影响原型示例与“下一次运行”，不控制任何真实进程。</div></div>
        <button type="button" class="icon" id="settings-close" aria-label="关闭设置"><svg width="12" height="12" viewBox="0 0 12 12" aria-hidden="true"><path d="M2 2l8 8M10 2l-8 8" stroke="currentColor" stroke-width="1.5"/></svg></button></div>
      <div class="dlg-b">
        <div class="sec-h"><b>角色配置</b><span>模型为可选的示例配置，不代表可用性或价格</span></div>
        <div class="list">
          ${Object.keys(ROLE).map((r) => `<div class="set-row"><span class="lbl">${ROLE[r]}${r === 'polish' ? '<small>你配置的最强配置；精修后必须复验</small>' : ''}</span>
            <span class="ctl"><select aria-label="${ROLE[r]} 运行时"><option>Pi</option></select>
            <select data-profile="${r}" aria-label="${ROLE[r]} 模型">${opt(MODELS, s.profiles[r])}</select></span></div>`).join('')}
        </div>
        <div class="sec-h"><b>调度</b></div>
        <div class="list">
          <div class="set-row"><label class="lbl" for="set-conc">本地并发<small>同时运行的 Pi 实例数</small></label><span class="ctl"><input id="set-conc" type="number" min="1" max="4" step="1" value="${s.concurrency}" data-setting="concurrency"></span></div>
          <div class="set-row"><label class="lbl" for="set-rounds">最大修复轮数<small>达到后停止并交给 Codex，不自动无限重试</small></label><span class="ctl"><input id="set-rounds" type="number" min="1" max="3" step="1" value="${s.maxRounds}" data-setting="maxRounds"></span></div>
          <div class="set-row"><label class="lbl" for="set-tok">单任务 token 预算<small>超出后暂停并提示</small></label><span class="ctl"><input id="set-tok" type="number" min="10000" step="10000" value="${s.tokenBudget}" data-setting="tokenBudget"></span></div>
          <div class="set-row"><label class="lbl" for="set-min">单次运行时限（分）</label><span class="ctl"><input id="set-min" type="number" min="5" max="240" step="5" value="${s.minuteBudget}" data-setting="minuteBudget"></span></div>
        </div>
        <div class="sec-h"><b>Pi 实例</b></div>
        <div class="list"><div class="set-row"><span class="lbl">Pi-01 · Pi-02 · Pi-03<small>可复用实例，每次运行按角色领取配置；不是“一个模型一个身份”</small></span></div></div>
        <div class="sec-h"><b>原型状态</b><span>用于核对状态约定</span></div>
        <div class="list"><div class="set-row"><span class="lbl" id="demo-lbl">示例状态</span>
          <span class="mini-seg" role="group" aria-labelledby="demo-lbl">
            ${[['normal', '正常'], ['empty', '无运行'], ['disconnected', '断连'], ['blocked', '阻塞']].map(([k, l]) => `<button type="button" data-demo="${k}" aria-pressed="${state.demo === k}">${l}</button>`).join('')}
          </span></div></div>
        <p class="applied" id="applied" role="status" aria-live="polite"></p>
      </div>`;
  }

  function openSettings() {
    state.returnFocus = $('#settings-btn');
    renderSettings();
    $('#settings-scrim').hidden = false;
    $('#settings-close').focus();
  }

  function applied(msg) { const el = $('#applied'); if (el) el.textContent = msg; }

  // ---------------------------------------------------------------------------
  // Render & events
  // ---------------------------------------------------------------------------
  function render() {
    renderSummary();
    ['agents', 'tasks', 'usage'].forEach((v) => { $(`#view-${v}`).hidden = state.view !== v; });
    document.querySelectorAll('#nav button').forEach((b) => {
      const on = b.dataset.view === state.view;
      b.classList.toggle('on', on);
      if (on) b.setAttribute('aria-current', 'page'); else b.removeAttribute('aria-current');
    });
    if (state.view === 'agents') renderAgents();
    if (state.view === 'tasks') renderTasks();
    if (state.view === 'usage') renderUsage();
    if (state.drawer) renderDrawer();
  }

  function setTheme(theme) {
    document.documentElement.dataset.theme = theme;
    $('#theme-btn').setAttribute('aria-label', theme === 'dark' ? '切换到浅色主题' : '切换到深色主题');
    try { localStorage.setItem('meerkat-proto-theme', theme); } catch (e) { /* storage unavailable */ }
  }

  document.addEventListener('click', (e) => {
    const t = e.target;
    const nav = t.closest('#nav button');
    if (nav) { state.view = nav.dataset.view; render(); return; }
    if (t.closest('#theme-btn')) { setTheme(document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark'); return; }
    if (t.closest('#settings-btn')) { openSettings(); return; }
    if (t.closest('#drawer-close')) { closeDialog('drawer'); return; }
    if (t.closest('#settings-close')) { closeDialog('settings'); return; }
    if (t.id === 'drawer-scrim') { closeDialog('drawer'); return; }
    if (t.id === 'settings-scrim') { closeDialog('settings'); return; }

    const openT = t.closest('[data-open-task]');
    if (openT) { openTask(openT.dataset.openTask, openT); return; }

    const stop = t.closest('[data-sim-stop]');
    if (stop) {
      const id = stop.dataset.simStop;
      if (state.sim[id] === 'stopped') { delete state.sim[id]; toast(`模拟：${id} 恢复为示例状态`); } else { state.sim[id] = 'stopped'; toast(`模拟：${id} 仅在本页标记暂停，未向任何进程发送信号`); }
      render();
      const again = document.querySelector(`[data-sim-stop="${id}"]`);
      if (again) again.focus();
      return;
    }
    if (t.closest('[data-sim-reconnect]')) { toast('模拟：已尝试重连 1 次，仍为断连示例；不会自动循环重试'); return; }

    const ag = t.closest('[data-agent]');
    if (ag) {
      const id = ag.dataset.agent;
      if (state.expanded.has(id)) state.expanded.delete(id); else state.expanded.add(id);
      render();
      const again = document.querySelector(`[data-agent="${id}"]`);
      if (again) again.focus();
      return;
    }
    const f = t.closest('[data-filter]');
    if (f) { state.filter = f.dataset.filter; render(); const b = document.querySelector(`[data-filter="${state.filter}"]`); if (b) b.focus(); return; }

    const tab = t.closest('[data-tab]');
    if (tab) { state.drawerTab = tab.dataset.tab; renderDrawer(); $(`#tab-${state.drawerTab}`).focus(); return; }
    if (t.closest('#issue-btn')) { state.issueOpen = !state.issueOpen; renderDrawer(); $('#issue-btn').focus(); return; }

    const demo = t.closest('[data-demo]');
    if (demo) {
      state.demo = demo.dataset.demo;
      state.sim = {};
      render();
      renderSettings();
      document.querySelector(`[data-demo="${state.demo}"]`).focus();
      applied(`示例状态已切换为“${demo.textContent}”，仅影响本页示例。`);
    }
  });

  document.addEventListener('input', (e) => {
    if (e.target.id === 'task-search') { state.query = e.target.value; renderTasks(); }
  });

  document.addEventListener('change', (e) => {
    const el = e.target;
    if (el.id === 'project') { state.project = el.value; render(); return; }
    if (el.dataset.profile) {
      state.settings.profiles[el.dataset.profile] = el.value;
      render();
      applied(`${ROLE[el.dataset.profile]}配置改为 ${el.value}：下一次运行生效，当前运行不受影响（原型）。`);
      return;
    }
    if (el.dataset.setting) {
      const min = Number(el.min) || 1;
      const max = el.max ? Number(el.max) : Infinity;
      let v = Math.round(Number(el.value));
      if (!Number.isFinite(v)) v = state.settings[el.dataset.setting];
      v = Math.min(max, Math.max(min, v));
      el.value = v;
      state.settings[el.dataset.setting] = v;
      render();
      applied(`已更新：${el.closest('.set-row').querySelector('.lbl').firstChild.textContent.trim()} = ${v}。仅影响原型示例与下一次调度。`);
    }
  });

  // tab arrow-key navigation inside the drawer
  document.addEventListener('keydown', (e) => {
    const tab = e.target.closest && e.target.closest('[role="tab"]');
    if (tab && (e.key === 'ArrowRight' || e.key === 'ArrowLeft')) {
      const i = TABS.findIndex(([k]) => k === state.drawerTab);
      const n = (i + (e.key === 'ArrowRight' ? 1 : TABS.length - 1)) % TABS.length;
      state.drawerTab = TABS[n][0];
      renderDrawer();
      $(`#tab-${state.drawerTab}`).focus();
      e.preventDefault();
      return;
    }
    const open = !$('#settings-scrim').hidden ? 'settings' : (!$('#drawer-scrim').hidden ? 'drawer' : null);
    if (!open) return;
    if (e.key === 'Escape') { e.preventDefault(); closeDialog(open); return; }
    if (e.key === 'Tab') {
      const box = open === 'drawer' ? $('#drawer') : $('#settings');
      const items = [...box.querySelectorAll('button, select, input, [href], [tabindex]:not([tabindex="-1"])')].filter((x) => !x.disabled && x.offsetParent !== null);
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      if (e.shiftKey && (document.activeElement === first || !box.contains(document.activeElement))) { last.focus(); e.preventDefault(); }
      else if (!e.shiftKey && (document.activeElement === last || !box.contains(document.activeElement))) { first.focus(); e.preventDefault(); }
    }
  });

  let saved = 'light';
  try { saved = localStorage.getItem('meerkat-proto-theme') || 'light'; } catch (e) { /* storage unavailable */ }
  setTheme(saved === 'dark' ? 'dark' : 'light');
  render();
})();

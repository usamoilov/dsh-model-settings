window.__ModuleLoader__.load({
  id: 'dsh-model-defaults',
  factory: (require) => {
    const React = require('react')
    const h = React.createElement
    const { useState, useEffect, useCallback } = React

    const NS = 'modelDefaults'
    const STATE_URL = '/api/model-defaults.state'
    const SAVE_URL = '/api/model-defaults.save'
    const RESTART_URL = '/api/model-defaults.restart'
    const TEST_SEARCH_URL = '/api/model-defaults.test-search'
    const SEP = '\u0001'
    const AUTO = 'auto'
    // what dsh-web-search-extra uses when its row sets no claudeModel
    const SEARCH_CLAUDE_DEFAULT = 'haiku'

    // ---- dictionaries: DSH requires en and zh; ru is also added as a UI language when DSH lacks it

    const en = {
      nav: 'Default model',
      title: 'Default model',
      lead: 'All models DSH uses, in one place: the default model of new conversations and headless runs, web search and subagents. DeepSeek can be turned off here completely.',
      loading: 'Loading…',
      loadFailed: 'Could not load: {error}',
      auto: 'Auto',
      model: 'Model',
      effort: 'Reasoning',
      effortDefault: 'model default',
      effortLabel: 'reasoning: {effort}',
      chooseModel: 'Choose a model',
      notInCatalog: '{model} — not in the catalog',
      catalogHint: 'Models come live from the DSH model catalog — every provider set up in Settings → Models (in the catalog: {count}).',
      failuresHint: 'Providers that failed to load: {list}',
      applyTitle: 'Where to apply the default model',
      'profile.web': 'Web UI — new conversations in every workspace',
      'profile.headless': 'Headless runs (profile headless: CLI, bots, schedulers)',
      'profile.other': 'Profile {name}',
      now: 'now: {value}',
      dsTitle: 'DeepSeek',
      dsOff: 'Turn DeepSeek models off',
      dsHint: 'The “DeepSeek” (API key) and “DeepSeek Account” providers disappear from model choices — in every profile at once.',
      dsState: '{profile}: models {state}',
      dsStateOff: 'off',
      dsStateOn: 'on',
      dsSearchWarn: 'Web search still goes through DeepSeek. To stop calling DeepSeek entirely, choose another search below.',
      searchTitle: 'Web search (web_search tool)',
      searchLead: 'Searching is done by a separate search service, not by the conversation model. Applies to every profile.',
      searchProvider: 'Search provider',
      notInstalled: 'not installed',
      'search.deepseek.title': 'DeepSeek',
      'search.deepseek.hint': 'DeepSeek API (DEEPSEEK_API_KEY): ~5 s, links only. DSH’s built-in default.',
      'search.searxng.title': 'SearXNG + {model}',
      'search.searxng.titlePlain': 'SearXNG',
      'search.searxng.hint': 'Your own SearXNG metasearch (Brave, Google, Wikipedia…): free and without limits; a DSH model writes a short answer from the results. Results in ~2 s; with an answer from a local model, up to a minute.',
      'search.claude.title': 'Claude via your Claude Code subscription',
      'search.claude.hint': 'Claude runs the query with its WebSearch tool: an answer with links, ~20–30 s, uses your Claude limits.',
      'search.auto.title': 'Auto',
      'search.auto.hint': 'Tries DeepSeek → SearXNG → Claude and takes the first answer. With DeepSeek models off it starts with SearXNG.',
      pluginMissing: 'The dsh-web-search-extra plugin is not installed in profile: {list}',
      answerBy: 'The short answer is written by',
      answerAuto: 'Auto — the DSH default model',
      summary: 'Write a short answer',
      summaryHint: 'Without it the search returns only links and snippets (~2 s), and the conversation model reads them.',
      sxOk: 'SearXNG is up ({url}, {ms} ms)',
      sxDown: 'SearXNG is unreachable ({url}): {error}',
      sxNoAnswer: 'no answer',
      claudeModel: 'Claude model for search',
      claudeAuto: 'Auto — Claude Code’s default',
      claudeAutoNamed: 'Auto — Claude Code’s default ({name})',
      claudeLive: 'Claude models available to this machine’s Claude Code login: {count}.',
      claudeFallback: 'Claude Code did not report its models ({reason}); showing the standard aliases.',
      claudeRefresh: 'Refresh',
      test: 'Test search',
      testing: 'Searching…',
      testSaveFirst: 'Save your choice first',
      testTitle: 'One real search through the web UI’s current provider',
      testAfterSave: 'test after saving',
      testQuery: 'latest stable Node.js version',
      testResult: '“{query}” — {seconds} s, sources: {count}',
      subTitle: 'Subagents',
      subLead: 'One model for all delegated work: the main agent hands substantive tasks to it — in the web UI and in headless runs.',
      subModel: 'Subagent model',
      subAuto: 'Auto — the conversation’s model',
      subClaudeGroup: 'Claude Code (Claude subscription)',
      subDshGroup: '{name} — DSH child agent',
      subClaudeAuto: 'Claude Code — its default model',
      subClaudeAutoNamed: 'Claude Code — its default model ({name})',
      subHintClaude: 'Tasks go to Claude Code through your subscription (subagent_claude_code tool). DSH child agents (subagent) keep the conversation’s model.',
      subHintAuto: 'Tasks go to a DSH child agent (subagent tool) on the model of the conversation that started it. The subagent_claude_code tool is turned off — Claude is not called.',
      subHintDsh: 'Tasks go to a DSH child agent (subagent tool) on this model. The subagent_claude_code tool is turned off — Claude is not called.',
      subNowClaude: 'Claude Code, {model}',
      subNowClaudeAuto: 'Claude Code, its default model',
      subNowDsh: '{model} (subagent)',
      subNowAuto: 'Auto — the conversation’s model (subagent)',
      overlayNow: '{label}: Claude Code, {model}',
      overlayError: '{label}: read error',
      infoSessions: 'Conversations already started keep their model — change it in that conversation’s composer.',
      save: 'Save',
      saving: 'Saving…',
      cancel: 'Cancel',
      'problem.chooseDefault': 'Choose the default model',
      'problem.applyNone': 'Tick at least one place to apply the model',
      'problem.notInWeb': 'This model is not in the web UI catalog',
      'problem.defaultDeepseek': 'The default model is DeepSeek, but DeepSeek is being turned off',
      'problem.searchNotInstalled': 'This search is not installed in profile: {list}',
      'problem.answerDeepseek': 'DeepSeek writes the search answer, but DeepSeek is being turned off',
      'problem.subDeepseek': 'Subagents run on DeepSeek, but DeepSeek is being turned off',
      restartText: 'The profile file changed, but the web UI model list is still the old one. Restart DSH Web (about a minute; running conversations are interrupted).',
      restartManual: 'The profile file changed, but the web UI model list is still the old one. Restart DSH Web to apply it.',
      restartBtn: 'Restart DSH Web',
      restarting: 'Restarting… the page reloads by itself.',
      restartLogin: 'DSH restarted. The browser session was reset — open DSH again through its login page.',
      restartTimeout: 'DSH did not answer within 2 minutes — check its service.',
      restartFailed: 'Failed: {error}',
    }

    const ru = {
      nav: 'Модель по умолчанию',
      title: 'Модель по умолчанию',
      lead: 'Модели DSH в одном месте: основная модель новых бесед и headless-запусков, веб-поиск, сабагенты. Здесь же можно полностью отключить DeepSeek.',
      loading: 'Загрузка…',
      loadFailed: 'Не удалось загрузить: {error}',
      auto: 'Авто',
      model: 'Модель',
      effort: 'Рассуждения',
      effortDefault: 'как у модели',
      effortLabel: 'рассуждения: {effort}',
      chooseModel: 'Выберите модель',
      notInCatalog: '{model} — нет в каталоге',
      catalogHint: 'Список берётся из живого каталога моделей DSH — все провайдеры, настроенные в «Настройки → Модели» (моделей: {count}).',
      failuresHint: 'Не загрузились провайдеры: {list}',
      applyTitle: 'Где применить модель по умолчанию',
      'profile.web': 'Веб-интерфейс — новые беседы во всех рабочих папках',
      'profile.headless': 'Headless-запуски (профиль headless: CLI, боты, расписания)',
      'profile.other': 'Профиль {name}',
      now: 'сейчас: {value}',
      dsTitle: 'DeepSeek',
      dsOff: 'Отключить модели DeepSeek',
      dsHint: 'Провайдеры «DeepSeek» (API-ключ) и «DeepSeek Account» пропадут из выбора моделей — во всех профилях сразу.',
      dsState: '{profile}: модели {state}',
      dsStateOff: 'отключены',
      dsStateOn: 'включены',
      dsSearchWarn: 'Веб-поиск всё ещё идёт через DeepSeek. Чтобы не обращаться к DeepSeek совсем, выберите ниже другой поиск.',
      searchTitle: 'Веб-поиск (инструмент web_search)',
      searchLead: 'Ищет не модель беседы, а отдельный поисковый сервис. Действует во всех профилях.',
      searchProvider: 'Поисковый провайдер',
      notInstalled: 'не установлен',
      'search.deepseek.title': 'DeepSeek',
      'search.deepseek.hint': 'Через API DeepSeek (ключ DEEPSEEK_API_KEY): ~5 с, только ссылки. Так устроено в DSH из коробки.',
      'search.searxng.title': 'SearXNG + {model}',
      'search.searxng.titlePlain': 'SearXNG',
      'search.searxng.hint': 'Свой метапоиск SearXNG (Brave, Google, Wikipedia…) — бесплатно и без лимитов; краткий ответ по выдаче пишет модель DSH. Выдача ~2 с, с ответом локальной модели — до минуты.',
      'search.claude.title': 'Claude по подписке Claude Code',
      'search.claude.hint': 'Запрос выполняет Claude с инструментом WebSearch: вывод и ссылки, ~20–30 с, расходует лимиты подписки Claude.',
      'search.auto.title': 'Авто',
      'search.auto.hint': 'По очереди DeepSeek → SearXNG → Claude: берётся первый, кто ответил. Когда модели DeepSeek отключены, начинает с SearXNG.',
      pluginMissing: 'Плагин dsh-web-search-extra не установлен в профиле: {list}',
      answerBy: 'Краткий ответ по выдаче пишет',
      answerAuto: 'Авто — модель по умолчанию DSH',
      summary: 'Писать краткий ответ',
      summaryHint: 'Без него поиск отдаёт только ссылки и фрагменты (~2 с), а читает их модель беседы.',
      sxOk: 'SearXNG работает ({url}, {ms} мс)',
      sxDown: 'SearXNG недоступен ({url}): {error}',
      sxNoAnswer: 'нет ответа',
      claudeModel: 'Модель Claude для поиска',
      claudeAuto: 'Авто — модель Claude Code по умолчанию',
      claudeAutoNamed: 'Авто — модель Claude Code по умолчанию ({name})',
      claudeLive: 'Моделей Claude, доступных входу Claude Code на этой машине: {count}.',
      claudeFallback: 'Claude Code не отдал список моделей ({reason}); показаны стандартные псевдонимы.',
      claudeRefresh: 'Обновить',
      test: 'Проверить поиск',
      testing: 'Ищу…',
      testSaveFirst: 'Сначала сохраните выбор',
      testTitle: 'Один настоящий поиск через текущий провайдер веб-интерфейса',
      testAfterSave: 'проверка — после сохранения',
      testQuery: 'последняя стабильная версия Node.js',
      testResult: '«{query}» — {seconds} с, источников: {count}',
      subTitle: 'Сабагенты',
      subLead: 'Одна модель для всех поручений: ей основной агент отдаёт содержательные задачи — и в веб-интерфейсе, и в headless-запусках.',
      subModel: 'Модель сабагентов',
      subAuto: 'Авто — та же модель, что у беседы',
      subClaudeGroup: 'Claude Code (подписка Claude)',
      subDshGroup: '{name} — дочерний агент DSH',
      subClaudeAuto: 'Claude Code — своя модель по умолчанию',
      subClaudeAutoNamed: 'Claude Code — своя модель по умолчанию ({name})',
      subHintClaude: 'Задачи выполняет Claude Code по подписке (инструмент subagent_claude_code). Дочерние агенты DSH (subagent) остаются на модели беседы.',
      subHintAuto: 'Задачи выполняет дочерний агент DSH (инструмент subagent) на той же модели, что у беседы, которая его запустила. Инструмент subagent_claude_code отключается — Claude не вызывается.',
      subHintDsh: 'Задачи выполняет дочерний агент DSH (инструмент subagent) на этой модели. Инструмент subagent_claude_code отключается — Claude не вызывается.',
      subNowClaude: 'Claude Code, {model}',
      subNowClaudeAuto: 'Claude Code, своя модель',
      subNowDsh: '{model} (subagent)',
      subNowAuto: 'Авто — модель беседы (subagent)',
      overlayNow: '{label}: Claude Code, {model}',
      overlayError: '{label}: ошибка чтения',
      infoSessions: 'Уже начатые беседы сохраняют свою модель — её меняют в поле ввода самой беседы.',
      save: 'Сохранить',
      saving: 'Сохраняю…',
      cancel: 'Отменить',
      'problem.chooseDefault': 'Выберите модель по умолчанию',
      'problem.applyNone': 'Отметьте хотя бы одно место, где применить модель',
      'problem.notInWeb': 'Этой модели нет в каталоге веб-интерфейса',
      'problem.defaultDeepseek': 'Модель по умолчанию — DeepSeek, а DeepSeek отключается',
      'problem.searchNotInstalled': 'Этот поиск не установлен в профиле: {list}',
      'problem.answerDeepseek': 'Ответ поиска пишет DeepSeek, а DeepSeek отключается',
      'problem.subDeepseek': 'Сабагенты работают на DeepSeek, а DeepSeek отключается',
      restartText: 'Файл профиля изменён, но список моделей веб-интерфейса ещё старый. Нужен перезапуск DSH Web (около минуты; беседы, которые сейчас выполняются, прервутся).',
      restartManual: 'Файл профиля изменён, но список моделей веб-интерфейса ещё старый. Перезапустите DSH Web, чтобы изменения вступили в силу.',
      restartBtn: 'Перезапустить DSH Web',
      restarting: 'Перезапускаю… страница обновится сама.',
      restartLogin: 'DSH перезапущен. Сессия браузера сброшена — откройте DSH заново через страницу входа.',
      restartTimeout: 'DSH не ответил за 2 минуты — проверьте его службу.',
      restartFailed: 'Не удалось: {error}',
    }

    const zh = {
      nav: '默认模型',
      title: '默认模型',
      lead: '集中管理 DSH 使用的所有模型：新对话和无界面运行的默认模型、网页搜索、子代理。也可以在这里完全关闭 DeepSeek。',
      loading: '加载中…',
      loadFailed: '加载失败：{error}',
      auto: '自动',
      model: '模型',
      effort: '推理',
      effortDefault: '模型默认',
      effortLabel: '推理：{effort}',
      chooseModel: '选择模型',
      notInCatalog: '{model} —— 不在模型目录中',
      catalogHint: '模型实时来自 DSH 模型目录 —— 即“设置 → 模型”中配置的所有提供方（共 {count} 个模型）。',
      failuresHint: '加载失败的提供方：{list}',
      applyTitle: '默认模型的应用范围',
      'profile.web': 'Web 界面 —— 所有工作区的新对话',
      'profile.headless': '无界面运行（配置档 headless：命令行、机器人、定时任务）',
      'profile.other': '配置档 {name}',
      now: '当前：{value}',
      dsTitle: 'DeepSeek',
      dsOff: '关闭 DeepSeek 模型',
      dsHint: '“DeepSeek”（API 密钥）和“DeepSeek Account”提供方将从模型选择中移除 —— 同时作用于所有配置档。',
      dsState: '{profile}：模型{state}',
      dsStateOff: '已关闭',
      dsStateOn: '已开启',
      dsSearchWarn: '网页搜索仍然通过 DeepSeek。若要完全不调用 DeepSeek，请在下方选择其他搜索方式。',
      searchTitle: '网页搜索（web_search 工具）',
      searchLead: '搜索由独立的搜索服务完成，而不是对话模型。适用于所有配置档。',
      searchProvider: '搜索提供方',
      notInstalled: '未安装',
      'search.deepseek.title': 'DeepSeek',
      'search.deepseek.hint': '通过 DeepSeek API（DEEPSEEK_API_KEY）：约 5 秒，仅返回链接。DSH 的内置默认方式。',
      'search.searxng.title': 'SearXNG + {model}',
      'search.searxng.titlePlain': 'SearXNG',
      'search.searxng.hint': '自建的 SearXNG 元搜索（Brave、Google、Wikipedia 等）：免费且无限额；由 DSH 模型根据结果撰写简短答案。结果约 2 秒；由本地模型撰写答案时最多约 1 分钟。',
      'search.claude.title': '通过 Claude Code 订阅使用 Claude',
      'search.claude.hint': 'Claude 使用其 WebSearch 工具执行查询：返回带链接的答案，约 20–30 秒，会消耗 Claude 订阅额度。',
      'search.auto.title': '自动',
      'search.auto.hint': '依次尝试 DeepSeek → SearXNG → Claude，采用第一个返回的答案。关闭 DeepSeek 模型后从 SearXNG 开始。',
      pluginMissing: '以下配置档未安装 dsh-web-search-extra 插件：{list}',
      answerBy: '撰写简短答案的模型',
      answerAuto: '自动 —— DSH 默认模型',
      summary: '撰写简短答案',
      summaryHint: '关闭后，搜索只返回链接和摘要（约 2 秒），由对话模型自行阅读。',
      sxOk: 'SearXNG 运行正常（{url}，{ms} 毫秒）',
      sxDown: 'SearXNG 无法访问（{url}）：{error}',
      sxNoAnswer: '无响应',
      claudeModel: '用于搜索的 Claude 模型',
      claudeAuto: '自动 —— Claude Code 的默认模型',
      claudeAutoNamed: '自动 —— Claude Code 的默认模型（{name}）',
      claudeLive: '本机 Claude Code 登录可用的 Claude 模型：{count} 个。',
      claudeFallback: 'Claude Code 未返回模型列表（{reason}）；显示标准别名。',
      claudeRefresh: '刷新',
      test: '测试搜索',
      testing: '搜索中…',
      testSaveFirst: '请先保存选择',
      testTitle: '通过 Web 界面当前的提供方执行一次真实搜索',
      testAfterSave: '保存后再测试',
      testQuery: 'Node.js 最新稳定版本',
      testResult: '“{query}” —— {seconds} 秒，来源：{count}',
      subTitle: '子代理',
      subLead: '所有委派工作使用同一个模型：主代理把实质性任务交给它 —— 在 Web 界面和无界面运行中都是如此。',
      subModel: '子代理模型',
      subAuto: '自动 —— 与对话相同的模型',
      subClaudeGroup: 'Claude Code（Claude 订阅）',
      subDshGroup: '{name} —— DSH 子代理',
      subClaudeAuto: 'Claude Code —— 使用其默认模型',
      subClaudeAutoNamed: 'Claude Code —— 使用其默认模型（{name}）',
      subHintClaude: '任务通过订阅交给 Claude Code（subagent_claude_code 工具）。DSH 子代理（subagent）沿用对话的模型。',
      subHintAuto: '任务交给 DSH 子代理（subagent 工具），使用发起它的对话的模型。subagent_claude_code 工具将被关闭 —— 不会调用 Claude。',
      subHintDsh: '任务交给使用此模型的 DSH 子代理（subagent 工具）。subagent_claude_code 工具将被关闭 —— 不会调用 Claude。',
      subNowClaude: 'Claude Code，{model}',
      subNowClaudeAuto: 'Claude Code，使用其默认模型',
      subNowDsh: '{model}（subagent）',
      subNowAuto: '自动 —— 对话的模型（subagent）',
      overlayNow: '{label}：Claude Code，{model}',
      overlayError: '{label}：读取错误',
      infoSessions: '已开始的对话保留各自的模型 —— 请在该对话的输入框中更改。',
      save: '保存',
      saving: '保存中…',
      cancel: '取消',
      'problem.chooseDefault': '请选择默认模型',
      'problem.applyNone': '请至少勾选一个应用范围',
      'problem.notInWeb': 'Web 界面的模型目录中没有此模型',
      'problem.defaultDeepseek': '默认模型是 DeepSeek，但正在关闭 DeepSeek',
      'problem.searchNotInstalled': '以下配置档未安装此搜索：{list}',
      'problem.answerDeepseek': '搜索答案由 DeepSeek 撰写，但正在关闭 DeepSeek',
      'problem.subDeepseek': '子代理使用 DeepSeek，但正在关闭 DeepSeek',
      restartText: '配置档文件已更改，但 Web 界面的模型列表仍是旧的。请重启 DSH Web（约 1 分钟；正在运行的对话会中断）。',
      restartManual: '配置档文件已更改，但 Web 界面的模型列表仍是旧的。请重启 DSH Web 以生效。',
      restartBtn: '重启 DSH Web',
      restarting: '正在重启…页面会自动刷新。',
      restartLogin: 'DSH 已重启。浏览器会话已重置 —— 请通过登录页重新打开 DSH。',
      restartTimeout: 'DSH 在 2 分钟内没有响应 —— 请检查其服务。',
      restartFailed: '失败：{error}',
    }

    const CSS = `
.dsh-md-root{max-width:720px;color:var(--dsw-alias-label-primary);font-size:13px;line-height:19px}
.dsh-md-title{font-size:16px;line-height:22px;font-weight:600;margin:0 0 4px}
.dsh-md-lead{color:var(--dsw-alias-label-secondary);margin:0 0 16px}
.dsh-md-block{border:1px solid var(--dsw-alias-border-l2);border-radius:10px;padding:12px 14px;margin-bottom:12px;background:var(--dsw-alias-bg-base)}
.dsh-md-h{font-size:13px;font-weight:600;margin:0 0 8px}
.dsh-md-sub{font-size:12px;font-weight:600;margin:10px 0 4px}
.dsh-md-row{display:flex;flex-wrap:wrap;gap:8px 12px;align-items:center}
.dsh-md-field{display:flex;flex-direction:column;gap:4px;min-width:0}
.dsh-md-field>span{font-size:12px;color:var(--dsw-alias-label-secondary)}
.dsh-md-select{min-width:220px;max-width:100%;height:32px;padding:0 8px;border:1px solid var(--dsw-alias-border-l2);border-radius:8px;background:var(--dsw-alias-bg-layer-1);color:var(--dsw-alias-label-primary);font:inherit}
.dsh-md-select:focus{outline:2px solid var(--dsw-alias-state-business-primary);outline-offset:1px}
.dsh-md-field>select{align-self:flex-start}
.dsh-md-check{display:flex;gap:8px;align-items:flex-start;margin:6px 0;cursor:pointer}
.dsh-md-check input{margin-top:3px;flex:none;accent-color:var(--dsw-alias-state-business-primary)}
.dsh-md-indent{margin-left:24px}
.dsh-md-hint{font-size:12px;color:var(--dsw-alias-label-tertiary)}
.dsh-md-warn{font-size:12px;color:var(--dsw-alias-state-warn-primary)}
.dsh-md-now{font-size:12px;color:var(--dsw-alias-label-tertiary);font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
.dsh-md-info{display:flex;gap:8px;font-size:12px;color:var(--dsw-alias-label-secondary);margin:6px 0}
.dsh-md-actions{display:flex;gap:8px;align-items:center;margin-top:4px}
.dsh-md-btn{height:32px;padding:0 14px;border-radius:8px;border:1px solid var(--dsw-alias-border-l2);background:var(--dsw-alias-bg-layer-1);color:var(--dsw-alias-label-primary);font:inherit;cursor:pointer}
.dsh-md-btn:hover:not(:disabled){background:var(--dsw-alias-interactive-bg-hover)}
.dsh-md-btn.is-primary{background:var(--dsw-alias-state-business-primary);border-color:transparent;color:#fff}
.dsh-md-btn.is-primary:hover:not(:disabled){filter:brightness(1.08);background:var(--dsw-alias-state-business-primary)}
.dsh-md-btn:disabled{opacity:.45;cursor:default}
.dsh-md-link{border:0;background:none;padding:0;font:inherit;font-size:12px;color:var(--dsw-alias-link, var(--dsw-alias-state-business-primary));cursor:pointer}
.dsh-md-link:hover:not(:disabled){text-decoration:underline}
.dsh-md-link:disabled{opacity:.45;cursor:default}
.dsh-md-msg{margin-top:10px;padding:8px 10px;border-radius:8px;font-size:12px;line-height:18px}
.dsh-md-msg.is-ok{background:var(--dsw-alias-bg-layer-1);border:1px solid var(--dsw-alias-border-l2)}
.dsh-md-msg.is-ok li::marker{color:var(--dsw-alias-state-success-primary)}
.dsh-md-msg.is-err{border:1px solid var(--dsw-alias-state-error-primary);color:var(--dsw-alias-state-error-primary)}
.dsh-md-msg.is-warn{border:1px solid var(--dsw-alias-state-warn-primary)}
.dsh-md-msg ul{margin:0;padding-left:18px}
.dsh-md-root a{color:var(--dsw-alias-link, var(--dsw-alias-state-business-primary));text-decoration:none}
.dsh-md-root a:hover{text-decoration:underline}
`

    function ensureStyle() {
      if (typeof document === 'undefined' || document.getElementById('dsh-model-defaults-style')) return
      const style = document.createElement('style')
      style.id = 'dsh-model-defaults-style'
      style.textContent = CSS
      document.head.appendChild(style)
    }

    const keyOf = (ref) => (ref === AUTO ? AUTO : ref && ref.provider ? `${ref.provider}${SEP}${ref.model}` : '')
    const refOf = (key) => {
      if (key === AUTO) return AUTO
      const [provider, model] = String(key).split(SEP)
      return provider && model ? { provider, model } : null
    }
    const isDeepseekKey = (key) => /^deepseek/i.test(String(key || ''))

    // one subagent choice: 'claude:<model>' (Claude Code tool), 'auto', or a DSH catalog key
    const CLAUDE_PREFIX = 'claude:'
    const isClaudeKey = (key) => String(key).startsWith(CLAUDE_PREFIX)
    function subKeyOf(subagents) {
      if (!subagents || subagents.route !== 'dsh') return CLAUDE_PREFIX + ((subagents && subagents.claudeCode && subagents.claudeCode.model) || AUTO)
      return subagents.dsh ? keyOf(subagents.dsh) : AUTO
    }
    function subChoice(key) {
      return isClaudeKey(key) ? { kind: 'claude', model: key.slice(CLAUDE_PREFIX.length) } : { kind: 'dsh', ref: refOf(key) }
    }

    function findModel(state, key) {
      const [provider, model] = String(key).split(SEP)
      const group = state.catalog.groups.find((g) => g.id === provider)
      return { provider, modelId: model, group, model: group ? group.models.find((m) => m.id === model) : null }
    }

    /** Replaced in apply(): the active UI language, sent to the host so its messages match. */
    let activeLocale = () => 'en'

    function request(url, body) {
      const headers = { accept: 'application/json', 'x-dsh-locale': activeLocale() }
      const init = body === undefined
        ? { headers }
        : { method: 'POST', headers: { ...headers, 'content-type': 'application/json', 'x-dsh-model-defaults': '1' }, body: JSON.stringify(body) }
      return fetch(url, init).then((r) => r.json().catch(() => ({ error: `HTTP ${r.status}` })).then((data) => {
        if (!r.ok || data.error) throw Object.assign(new Error(data.error || `HTTP ${r.status}`), { status: r.status })
        return data
      }))
    }

    /** Draft of the form from a server state. */
    function draftFrom(data) {
      const profiles = Object.values(data.profiles)
      const main = data.profiles[data.running] || profiles[0] || {}
      const current = main.defaultModel || null
      const search = main.search || {}
      return {
        key: current && current.provider ? `${current.provider}${SEP}${current.model}` : '',
        effort: current && current.reasoningEffort ? current.reasoningEffort : '',
        apply: Object.fromEntries(profiles.map((p) => [p.name, true])),
        dsModels: profiles.length > 0 && profiles.every((p) => p.deepseek && p.deepseek.models),
        search: search.provider || 'deepseek-official',
        claudeSearch: search.claudeModel || SEARCH_CLAUDE_DEFAULT,
        summaryKey: search.summaryModel ? keyOf(search.summaryModel) : AUTO,
        summary: search.summary !== false,
        sub: subKeyOf(main.subagents),
      }
    }

    /** The page body; `t` is the slot's locale seat for this plugin's namespace. */
    function ModelDefaultsSection({ t }) {
      const [state, setState] = useState(null)
      const [draft, setDraft] = useState(null)
      const [initial, setInitial] = useState(null)
      const [loadError, setLoadError] = useState(null)
      const [busy, setBusy] = useState(false)
      const [refreshing, setRefreshing] = useState(false)
      const [result, setResult] = useState(null)
      const [restart, setRestart] = useState(null)
      const [testing, setTesting] = useState(null)

      const load = useCallback(() => {
        setLoadError(null)
        return request(STATE_URL).then((data) => {
          setState(data)
          const d = draftFrom(data)
          setDraft(d)
          setInitial(d)
        }).catch((error) => setLoadError(error.message))
      }, [])

      useEffect(() => {
        ensureStyle()
        load()
      }, [load])

      if (loadError) return h('div', { className: 'dsh-md-root' }, h('div', { className: 'dsh-md-msg is-err' }, t('loadFailed', { error: loadError })))
      if (!state || !draft) return h('div', { className: 'dsh-md-root' }, h('div', { className: 'dsh-md-hint' }, t('loading')))

      const set = (patch) => {
        setDraft((prev) => ({ ...prev, ...patch }))
        setResult(null)
      }
      const label = (sel) => {
        if (!sel || !sel.provider) return '—'
        return `${sel.provider} / ${sel.model}${sel.reasoningEffort ? ` · ${t('effortLabel', { effort: sel.reasoningEffort })}` : ''}`
      }
      const modelName = (ref) => {
        if (!ref || ref === AUTO) return null
        const found = findModel(state, keyOf(ref))
        return (found.model && found.model.name) || `${ref.provider} / ${ref.model}`
      }

      const profiles = Object.values(state.profiles)
      const groups = state.catalog.groups.filter((g) => !(draft.dsModels && g.deepseek))
      const modelCount = state.catalog.groups.reduce((n, g) => n + g.models.length, 0)
      const picked = findModel(state, draft.key)
      const knownKey = groups.some((g) => g.id === picked.provider && g.models.some((m) => m.id === picked.modelId))
      const efforts = picked.model && picked.model.reasoning ? picked.model.reasoning.efforts : []

      // Claude models: Claude Code's own list (or the fallback aliases); a stored full id
      // such as claude-haiku-5-5 is matched against each alias's resolved model
      const claude = state.claude || { models: [], recommended: null, source: 'fallback' }
      const claudeFind = (id) => claude.models.find((m) => m.id === id) || claude.models.find((m) => m.model === id) || null
      const claudeLabel = (id) => {
        if (!id || id === AUTO) return null
        const found = claudeFind(id)
        return found ? found.name : id
      }
      const recommendedName = claude.recommended
        ? claudeLabel(claude.recommended.model) || claude.recommended.model || claude.recommended.name
        : null

      // only the parts that changed are sent: e.g. toggling DeepSeek alone must not
      // overwrite each profile's own default (a headless profile may keep reasoningEffort: off)
      const modelDirty = draft.key !== initial.key || draft.effort !== initial.effort || JSON.stringify(draft.apply) !== JSON.stringify(initial.apply)
      const searchDirty = ['search', 'claudeSearch', 'summaryKey', 'summary'].some((k) => draft[k] !== initial[k])
      const subDirty = draft.sub !== initial.sub
      const dirty = modelDirty || searchDirty || subDirty || draft.dsModels !== initial.dsModels
      const searchBlocked = profiles.filter((p) => !(p.searchAvailable || []).includes(draft.search)).map((p) => p.name)
      const usesSearxng = draft.search === 'searxng' || draft.search === AUTO
      const usesClaude = draft.search === 'claude-code' || draft.search === AUTO
      const summaryRef = refOf(draft.summaryKey)

      let problem = null
      if (modelDirty && !draft.key) problem = t('problem.chooseDefault')
      else if (modelDirty && !profiles.some((p) => draft.apply[p.name])) problem = t('problem.applyNone')
      else if (modelDirty && !knownKey && draft.apply[state.running]) problem = t('problem.notInWeb')
      else if (draft.dsModels && isDeepseekKey(picked.provider) && (modelDirty || draft.apply[state.running])) problem = t('problem.defaultDeepseek')
      else if (searchDirty && searchBlocked.length) problem = t('problem.searchNotInstalled', { list: searchBlocked.join(', ') })
      else if (draft.dsModels && usesSearxng && draft.summary && summaryRef !== AUTO && isDeepseekKey(draft.summaryKey)) problem = t('problem.answerDeepseek')
      else if (draft.dsModels && isDeepseekKey(draft.sub)) problem = t('problem.subDeepseek')

      const onSave = () => {
        setBusy(true)
        setResult(null)
        setTesting(null)
        request(SAVE_URL, {
          selection: modelDirty ? { provider: picked.provider, model: picked.modelId, reasoningEffort: draft.effort || null } : null,
          apply: draft.apply,
          deepseek: { models: draft.dsModels },
          search: searchDirty ? { provider: draft.search, claudeModel: draft.claudeSearch, summaryModel: summaryRef, summary: draft.summary } : null,
          subagents: subDirty ? subChoice(draft.sub) : null,
        }).then((data) => {
          setState(data.state)
          const d = draftFrom(data.state)
          setDraft(d)
          setInitial(d)
          setResult({ ok: true, notes: data.notes || [] })
        }).catch((error) => setResult({ ok: false, notes: [error.message] }))
          .finally(() => setBusy(false))
      }

      // asks Claude Code for its model list again; keeps the user's unsaved draft
      const onRefreshClaude = () => {
        setRefreshing(true)
        request(`${STATE_URL}?refreshClaude=1`)
          .then((data) => setState((prev) => ({ ...prev, claude: data.claude })))
          .catch((error) => setResult({ ok: false, notes: [error.message] }))
          .finally(() => setRefreshing(false))
      }

      const onRestart = () => {
        setRestart('running')
        request(RESTART_URL, {}).then(() => {
          const started = Date.now()
          const poll = () => {
            if (Date.now() - started > 120000) {
              setRestart('timeout')
              return
            }
            fetch(STATE_URL, { headers: { accept: 'application/json' } }).then((r) => {
              if (r.status === 401) setRestart('login')
              else if (r.ok && Date.now() - started > 4000) window.location.reload()
              else setTimeout(poll, 3000)
            }).catch(() => setTimeout(poll, 3000))
          }
          setTimeout(poll, 3000)
        }).catch((error) => setRestart(`error:${error.message}`))
      }

      const onTestSearch = () => {
        setTesting({ running: true })
        request(TEST_SEARCH_URL, { query: t('testQuery') })
          .then((data) => setTesting({ data }))
          .catch((error) => setTesting({ error: error.message }))
      }

      const sx = state.searxng || {}
      const modelOption = (g, m) => h('option', { key: m.id, value: `${g.id}${SEP}${m.id}` }, m.name && m.name !== m.id ? `${m.name} (${m.id})` : m.id)
      const claudeOption = (m, prefix) => h('option', { key: m.id, value: prefix + m.id, title: m.description || undefined }, m.name)
      // a stored value Claude Code does not list as such (e.g. a full model id): keep it selectable
      const claudeExtra = (value, prefix) => {
        const id = value.slice(prefix.length)
        if (id === AUTO || claude.models.some((m) => m.id === id)) return null
        const found = claudeFind(id)
        return h('option', { value }, found ? `${found.name} (${id})` : id)
      }

      /** Select over the DSH catalog, optionally with «Auto» first. */
      const modelSelect = (value, onChange, autoText) => {
        const known = value === AUTO || groups.some((g) => g.models.some((m) => `${g.id}${SEP}${m.id}` === value))
        return h('select', { className: 'dsh-md-select', value, onChange: (e) => onChange(e.target.value) },
          autoText ? h('option', { value: AUTO }, autoText) : null,
          !known && value ? h('option', { value, disabled: true }, t('notInCatalog', { model: label(refOf(value)) })) : null,
          !value ? h('option', { value: '' }, t('chooseModel')) : null,
          groups.map((g) => h('optgroup', { key: g.id, label: g.name || g.id }, g.models.map((m) => modelOption(g, m)))))
      }

      /** Claude model for search: «Auto» (Claude Code's own default), then the models it reports. */
      const claudeSelect = (value, onChange) => h('select', { className: 'dsh-md-select', value, onChange: (e) => onChange(e.target.value) },
        h('option', { value: AUTO }, recommendedName ? t('claudeAutoNamed', { name: recommendedName }) : t('claudeAuto')),
        claudeExtra(value, ''),
        claude.models.map((m) => claudeOption(m, '')))

      /** The single subagent select: «Auto», Claude Code models, then the DSH catalog. */
      const subagentSelect = (value, onChange) => {
        const knownDsh = !value || value === AUTO || isClaudeKey(value) || groups.some((g) => g.models.some((m) => `${g.id}${SEP}${m.id}` === value))
        return h('select', { className: 'dsh-md-select', value, onChange: (e) => onChange(e.target.value) },
          h('option', { value: AUTO }, t('subAuto')),
          !knownDsh ? h('option', { value, disabled: true }, t('notInCatalog', { model: label(refOf(value)) })) : null,
          h('optgroup', { label: t('subClaudeGroup') },
            h('option', { value: CLAUDE_PREFIX + AUTO }, recommendedName ? t('subClaudeAutoNamed', { name: recommendedName }) : t('subClaudeAuto')),
            isClaudeKey(value) ? claudeExtra(value, CLAUDE_PREFIX) : null,
            claude.models.map((m) => claudeOption(m, CLAUDE_PREFIX))),
          groups.map((g) => h('optgroup', { key: g.id, label: t('subDshGroup', { name: g.name || g.id }) }, g.models.map((m) => modelOption(g, m)))))
      }

      const subLabel = (subagents) => {
        if (!subagents) return '—'
        if (subagents.route !== 'dsh') {
          const model = subagents.claudeCode && subagents.claudeCode.model
          return model && model !== AUTO ? t('subNowClaude', { model: claudeLabel(model) }) : t('subNowClaudeAuto')
        }
        return subagents.dsh && subagents.dsh !== AUTO ? t('subNowDsh', { model: label(subagents.dsh) }) : t('subNowAuto')
      }

      const searchLabel = (p) => {
        const s = p.search || {}
        if (s.provider === 'claude-code') return `Claude (${claudeLabel(s.claudeModel || SEARCH_CLAUDE_DEFAULT) || t('auto')})`
        if (s.provider === 'searxng') return `SearXNG (${s.summary === false ? t('sxNoAnswer') : (modelName(s.summaryModel) || t('auto'))})`
        if (s.provider === AUTO) return t('auto')
        return (state.searchProviders[s.provider] || {}).label || s.provider || '—'
      }

      const answerName = summaryRef === AUTO ? (picked.model && picked.model.name) || null : modelName(summaryRef)
      const searchOptions = [
        { id: 'deepseek-official', title: t('search.deepseek.title'), hint: t('search.deepseek.hint') },
        { id: 'searxng', title: draft.summary && answerName ? t('search.searxng.title', { model: answerName }) : t('search.searxng.titlePlain'), hint: t('search.searxng.hint') },
        { id: 'claude-code', title: t('search.claude.title'), hint: t('search.claude.hint') },
        { id: AUTO, title: t('search.auto.title'), hint: t('search.auto.hint') },
      ]
      // a provider set by hand that this page does not know: keep it shown as the current value
      if (!searchOptions.some((o) => o.id === draft.search)) {
        searchOptions.push({ id: draft.search, title: (state.searchProviders[draft.search] || {}).label || draft.search, hint: '' })
      }
      const searchChoice = searchOptions.find((o) => o.id === draft.search)
      const profileLabel = (p) => (p.name === 'web' ? t('profile.web') : p.name === 'headless' ? t('profile.headless') : t('profile.other', { name: p.name }))
      // profiles lacking an extra search plugin; only for the providers this page knows (DeepSeek is built in)
      const missingFor = (id) => (id === 'deepseek-official' || !Object.hasOwn(state.searchProviders, id) ? [] : profiles.filter((p) => !(p.searchAvailable || []).includes(id)).map((p) => p.name))
      const usesClaudeSub = profiles.some((p) => p.subagents && p.subagents.route !== 'dsh')

      return h('div', { className: 'dsh-md-root' },
        h('h2', { className: 'dsh-md-title' }, t('title')),
        h('p', { className: 'dsh-md-lead' }, t('lead')),

        // ---- default model
        h('div', { className: 'dsh-md-block' },
          h('div', { className: 'dsh-md-row' },
            h('label', { className: 'dsh-md-field' },
              h('span', null, t('model')),
              modelSelect(draft.key, (value) => {
                const next = findModel(state, value)
                set({ key: value, effort: next.model && next.model.reasoning ? next.model.reasoning.defaultEffort || '' : '' })
              })),
            efforts.length ? h('label', { className: 'dsh-md-field' },
              h('span', null, t('effort')),
              h('select', { className: 'dsh-md-select', style: { minWidth: 140 }, value: draft.effort, onChange: (e) => set({ effort: e.target.value }) },
                h('option', { value: '' }, t('effortDefault')),
                efforts.map((e) => h('option', { key: e.id, value: e.id }, e.name || e.id)))) : null),
          h('div', { className: 'dsh-md-hint', style: { marginTop: 6 } }, t('catalogHint', { count: modelCount })),
          state.catalog.failures.length
            ? h('div', { className: 'dsh-md-warn', style: { marginTop: 4 } }, t('failuresHint', { list: state.catalog.failures.map((f) => f.name || f.id).join(', ') }))
            : null),

        h('div', { className: 'dsh-md-block' },
          h('div', { className: 'dsh-md-h' }, t('applyTitle')),
          profiles.map((p) => h('label', { key: p.name, className: 'dsh-md-check' },
            h('input', { type: 'checkbox', checked: !!draft.apply[p.name], onChange: (e) => set({ apply: { ...draft.apply, [p.name]: e.target.checked } }) }),
            h('span', null,
              h('div', null, profileLabel(p)),
              h('div', { className: 'dsh-md-now' }, `${t('now', { value: label(p.defaultModel) })}${p.running ? '' : ` · ${p.path}`}`),
              p.error ? h('div', { className: 'dsh-md-warn' }, p.error) : null)))),

        // ---- DeepSeek
        h('div', { className: 'dsh-md-block' },
          h('div', { className: 'dsh-md-h' }, t('dsTitle')),
          h('label', { className: 'dsh-md-check' },
            h('input', { type: 'checkbox', checked: draft.dsModels, onChange: (e) => set({ dsModels: e.target.checked }) }),
            h('span', null,
              h('div', null, t('dsOff')),
              h('div', { className: 'dsh-md-hint' }, t('dsHint')))),
          h('div', { className: 'dsh-md-now', style: { marginTop: 4 } },
            profiles.map((p) => t('dsState', { profile: p.name, state: t(p.deepseek && p.deepseek.models ? 'dsStateOff' : 'dsStateOn') })).join(' · ')),
          draft.dsModels && draft.search === 'deepseek-official'
            ? h('div', { className: 'dsh-md-warn', style: { marginTop: 6 } }, t('dsSearchWarn'))
            : null),

        // ---- web search
        h('div', { className: 'dsh-md-block' },
          h('div', { className: 'dsh-md-h' }, t('searchTitle')),
          h('div', { className: 'dsh-md-hint', style: { marginBottom: 6 } }, t('searchLead')),
          h('label', { className: 'dsh-md-field' },
            h('span', null, t('searchProvider')),
            h('select', { className: 'dsh-md-select', style: { minWidth: 320 }, value: draft.search, onChange: (e) => set({ search: e.target.value }) },
              searchOptions.map((o) => h('option', { key: o.id, value: o.id },
                missingFor(o.id).length ? `${o.title} — ${t('notInstalled')}` : o.title)))),
          searchChoice && searchChoice.hint ? h('div', { className: 'dsh-md-hint', style: { marginTop: 6 } }, searchChoice.hint) : null,
          missingFor(draft.search).length
            ? h('div', { className: 'dsh-md-warn', style: { marginTop: 4 } }, t('pluginMissing', { list: missingFor(draft.search).join(', ') }))
            : null,
          usesSearxng ? h('div', { className: 'dsh-md-indent' },
            h('div', { className: 'dsh-md-sub' }, 'SearXNG'),
            h('div', { className: 'dsh-md-row' },
              h('label', { className: 'dsh-md-field' },
                h('span', null, t('answerBy')),
                modelSelect(draft.summaryKey, (value) => set({ summaryKey: value }), t('answerAuto')))),
            h('label', { className: 'dsh-md-check' },
              h('input', { type: 'checkbox', checked: draft.summary, onChange: (e) => set({ summary: e.target.checked }) }),
              h('span', null, t('summary'), h('div', { className: 'dsh-md-hint' }, t('summaryHint')))),
            h('div', { className: sx.ok ? 'dsh-md-hint' : 'dsh-md-warn' },
              sx.ok ? t('sxOk', { url: sx.url, ms: sx.ms }) : t('sxDown', { url: sx.url, error: sx.error || t('sxNoAnswer') }))) : null,
          usesClaude ? h('div', { className: 'dsh-md-indent' },
            h('div', { className: 'dsh-md-sub' }, 'Claude'),
            h('label', { className: 'dsh-md-field' },
              h('span', null, t('claudeModel')),
              claudeSelect(draft.claudeSearch, (value) => set({ claudeSearch: value })))) : null,
          h('div', { className: 'dsh-md-now', style: { marginTop: 8 } }, profiles.map((p) => `${p.name}: ${searchLabel(p)}`).join(' · ')),
          h('div', { className: 'dsh-md-actions', style: { marginTop: 8 } },
            h('button', {
              type: 'button', className: 'dsh-md-btn', disabled: (testing && testing.running) || searchDirty, onClick: onTestSearch,
              title: searchDirty ? t('testSaveFirst') : t('testTitle'),
            }, testing && testing.running ? t('testing') : t('test')),
            searchDirty ? h('span', { className: 'dsh-md-hint' }, t('testAfterSave')) : null),
          testing && testing.error ? h('div', { className: 'dsh-md-msg is-err' }, testing.error) : null,
          testing && testing.data ? h('div', { className: 'dsh-md-msg is-ok' },
            h('div', { style: { marginBottom: 4 } }, t('testResult', { query: testing.data.query, seconds: Math.round(testing.data.ms / 100) / 10, count: testing.data.sources.length })),
            testing.data.content ? h('div', { style: { marginBottom: 4 } }, testing.data.content) : null,
            h('ul', null, testing.data.sources.map((s, i) => h('li', { key: i },
              h('a', { href: s.url, target: '_blank', rel: 'noreferrer noopener' }, s.title || s.url))))) : null),

        // ---- subagents
        h('div', { className: 'dsh-md-block' },
          h('div', { className: 'dsh-md-h' }, t('subTitle')),
          h('div', { className: 'dsh-md-hint', style: { marginBottom: 6 } }, t('subLead')),
          h('label', { className: 'dsh-md-field' },
            h('span', null, t('subModel')),
            subagentSelect(draft.sub, (value) => set({ sub: value }))),
          h('div', { className: 'dsh-md-hint', style: { marginTop: 6 } },
            isClaudeKey(draft.sub) ? t('subHintClaude') : draft.sub === AUTO ? t('subHintAuto') : t('subHintDsh')),
          h('div', { className: 'dsh-md-now', style: { marginTop: 4 } },
            [...profiles.map((p) => `${p.name}: ${subLabel(p.subagents)}`),
              ...(usesClaudeSub ? (state.overlays || []).map((o) => (o.error
                ? t('overlayError', { label: o.label })
                : t('overlayNow', { label: o.label, model: claudeLabel(o.claudeModel) || t('auto') }))) : [])].join(' · ')),
          h('div', { className: 'dsh-md-hint', style: { marginTop: 6 } },
            claude.source === 'claude-code' ? t('claudeLive', { count: claude.models.length }) : t('claudeFallback', { reason: claude.reason || '—' }),
            ' ',
            h('button', { type: 'button', className: 'dsh-md-link', disabled: refreshing, onClick: onRefreshClaude }, refreshing ? '…' : t('claudeRefresh')))),

        h('div', { className: 'dsh-md-info' }, h('span', null, 'ⓘ'), h('span', null, t('infoSessions'))),

        h('div', { className: 'dsh-md-actions' },
          h('button', { type: 'button', className: 'dsh-md-btn is-primary', disabled: busy || !dirty || !!problem, onClick: onSave }, busy ? t('saving') : t('save')),
          h('button', { type: 'button', className: 'dsh-md-btn', disabled: busy || !dirty, onClick: () => { setDraft(initial); setResult(null) } }, t('cancel')),
          dirty && problem ? h('span', { className: 'dsh-md-hint' }, problem) : null),

        result ? h('div', { className: `dsh-md-msg ${result.ok ? 'is-ok' : 'is-err'}`, role: 'status' },
          h('ul', null, result.notes.map((n, i) => h('li', { key: i }, n)))) : null,

        state.restartNeeded ? h('div', { className: 'dsh-md-msg is-warn' },
          h('div', { style: { marginBottom: state.canRestart ? 6 : 0 } }, state.canRestart ? t('restartText') : t('restartManual')),
          !state.canRestart ? null
            : restart === null ? h('button', { type: 'button', className: 'dsh-md-btn', onClick: onRestart }, t('restartBtn'))
              : restart === 'running' ? h('span', null, t('restarting'))
                : restart === 'login' ? h('span', null, t('restartLogin'))
                  : restart === 'timeout' ? h('span', null, t('restartTimeout'))
                    : h('span', null, t('restartFailed', { error: restart.replace(/^error:/, '') }))) : null)
    }

    const inject = ['slots', 'locale']

    function apply(ctx) {
      ensureStyle()
      ctx.effect(() => ctx.locale.register(NS, { en, zh, ru }), 'dsh-model-defaults: dictionaries')
      // DSH before 0.2.2 has no Russian UI language; add it (missing keys fall back to English)
      const hasRu = (ctx.locale.getSnapshot().locales || []).some((l) => String(l.id).toLowerCase() === 'ru')
      if (!hasRu) ctx.effect(() => ctx.locale.addLanguage({ id: 'ru', label: 'Русский', fallback: 'en' }), 'dsh-model-defaults: Russian language')
      activeLocale = () => ctx.locale.getSnapshot().active
      const t = ctx.locale.bind(NS)
      ctx.slots.inject('settings.section', () => ctx.slots.register({
        name: 'settings.section',
        id: 'model-defaults',
        order: 11,
        label: () => t('nav'),
        locale: NS,
      }, ModelDefaultsSection))
    }

    return { apply, inject }
  },
})

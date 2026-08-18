package webui

import "net/http"

const agentConfigScript = `
(() => {
  const openAdvancedFromHash = () => {
    if (window.location.hash === '#advanced') {
      const panel = document.querySelector('.advanced-config, .advanced-studio');
      if (panel) {
        panel.open = true;
        window.requestAnimationFrame(() => panel.scrollIntoView({block: 'start'}));
      }
    }
  };

  const setDisabled = (input, disabled) => {
    if (!input) return;
    input.disabled = disabled;
    input.closest('label')?.classList.toggle('is-disabled', disabled);
  };

  const bindServerForm = () => {
    const form = document.querySelector('form.server-form');
    if (!form) return;
    const transport = form.querySelector('select[name="transport"]');
    const transportPath = form.querySelector('input[name="transport_path"]');
    const tls = form.querySelector('input[name="tls_enabled"][type="checkbox"]');
    const certificate = form.querySelector('input[name="certificate_path"]');
    const privateKey = form.querySelector('input[name="private_key_path"]');
    const method = form.querySelector('select[name="method"]');
    const credential = form.querySelector('input[name="credential"]');

    const applyTransport = () => {
      const raw = !transport || transport.value === 'raw';
      setDisabled(transportPath, raw);
      if (transportPath) {
        transportPath.required = !raw;
        if (raw) transportPath.value = '';
      }
    };
    const applyTLS = () => {
      const enabled = !tls || tls.checked;
      setDisabled(certificate, !enabled);
      setDisabled(privateKey, !enabled);
      if (certificate) certificate.required = enabled;
      if (privateKey) privateKey.required = enabled;
    };
    const randomCredential = (bytes) => {
      if (!window.crypto?.getRandomValues) return;
      const values = new Uint8Array(bytes);
      window.crypto.getRandomValues(values);
      let binary = '';
      values.forEach((value) => { binary += String.fromCharCode(value); });
      return window.btoa(binary);
    };
    const applyMethod = () => {
      if (!method || !credential) return;
      const wantedBytes = method.value === '2022-blake3-aes-128-gcm' ? 16 : 32;
      try {
        const decoded = window.atob(credential.value);
        if (decoded.length !== wantedBytes) credential.value = randomCredential(wantedBytes) || credential.value;
      } catch (_) {
        credential.value = randomCredential(wantedBytes) || credential.value;
      }
    };
    transport?.addEventListener('change', applyTransport);
    tls?.addEventListener('change', applyTLS);
    method?.addEventListener('change', applyMethod);
    applyTransport();
    applyTLS();
  };

  const bindCoreVersionForms = () => {
    document.querySelectorAll('.core-version-form').forEach((form) => {
      const channel = form.querySelector('select[name="release_channel"]');
      const version = form.querySelector('input[name="custom_version"]');
      const apply = () => {
        const custom = channel?.value === 'custom';
        setDisabled(version, !custom);
        if (version) version.required = custom;
      };
      channel?.addEventListener('change', apply);
      apply();
    });
  };

  const bindProfileEditor = () => {
    const form = document.querySelector('[data-profile-editor]');
    if (!form || form.dataset.newConfig !== '1') return;
    const engine = form.querySelector('select[name="engine"]');
    const content = form.querySelector('textarea[name="content"]');
    if (!engine || !content) return;
    const editor = form.querySelector('[data-code-editor]');
    const fileName = editor?.querySelector('.code-file-meta b');
    const language = editor?.querySelector('.code-language');
    const badge = form.closest('.config-workspace')?.querySelector('.editor-toolbar-state .engine-badge');
    const defaults = {
      'mihomo': 'mixed-port: 7890\nallow-lan: false\nmode: rule\nlog-level: info\nproxies: []\nproxy-groups: []\nrules:\n  - MATCH,DIRECT\n',
      'xray': '{\n  "log": {"loglevel": "warning"},\n  "inbounds": [],\n  "outbounds": []\n}\n',
      'sing-box': '{\n  "$schema": "https://sing-box.sagernet.org/schema.json",\n  "log": {"level": "info"},\n  "inbounds": [],\n  "outbounds": []\n}\n',
      'ss-rust': '{\n  "server": "127.0.0.1",\n  "server_port": 8388,\n  "password": "change-this-password",\n  "method": "chacha20-ietf-poly1305",\n  "mode": "tcp_and_udp",\n  "timeout": 300,\n  "no_delay": true\n}\n'
    };
    let previous = form.dataset.engine || engine.value;
    let edited = content.value.trim() !== (defaults[previous] || '').trim();
    content.addEventListener('input', () => { edited = true; });
    engine.addEventListener('change', () => {
      const previousDefault = defaults[previous] || '';
      let replacedDefault = false;
      if (!edited || content.value.trim() === previousDefault.trim()) {
        content.value = defaults[engine.value] || '';
        edited = false;
        replacedDefault = true;
      }
      previous = engine.value;
      form.dataset.engine = previous;
      const yaml = previous === 'mihomo';
      const displayName = previous === 'mihomo' ? 'Mihomo' : (previous === 'xray' ? 'Xray' : (previous === 'sing-box' ? 'sing-box' : 'Shadowsocks Rust'));
      if (editor) editor.dataset.codeLanguage = yaml ? 'YAML' : 'JSON';
      if (fileName) fileName.textContent = yaml ? 'config.yaml' : 'config.json';
      if (language) language.textContent = yaml ? 'YAML' : 'JSON';
      if (badge) {
        badge.classList.remove('mihomo', 'xray', 'sing-box', 'ss-rust');
        badge.classList.add(previous);
        badge.textContent = displayName;
      }
      content.setAttribute('aria-label', displayName + ' 配置档案源码');
      if (replacedDefault) content.setSelectionRange(0, 0);
      content.dispatchEvent(new CustomEvent('code-editor-baseline', {bubbles: true, detail: {status: '草稿', value: defaults[previous] || ''}}));
    });
  };

  const bindConfirmations = () => {
	const dialog = document.querySelector('[data-confirm-dialog]');
	const message = dialog?.querySelector('[data-confirm-message]');
	const accept = dialog?.querySelector('[data-confirm-accept]');
	const cancel = dialog?.querySelector('[data-confirm-cancel]');
	let pendingForm = null;
	let pendingSubmitter = null;
	const confirmationFor = (form, submitter) => {
	  const submitterMessage = submitter?.dataset.confirmSubmit;
	  if (submitterMessage) return submitterMessage;
	  const formMessage = form.dataset.confirm;
	  if (!formMessage) return '';
	  const requiredAction = form.dataset.confirmAction;
	  if (requiredAction && form.querySelector('[name="action"]')?.value !== requiredAction) return '';
	  return formMessage;
	};
	document.querySelectorAll('form').forEach((form) => {
	  form.addEventListener('submit', (event) => {
		if (form.dataset.confirmed === '1') {
		  delete form.dataset.confirmed;
		  return;
		}
		const confirmation = confirmationFor(form, event.submitter);
		if (!confirmation) return;
		if (!dialog?.showModal || !message || !accept) {
		  if (!window.confirm(confirmation)) event.preventDefault();
		  return;
		}
		event.preventDefault();
		pendingForm = form;
		pendingSubmitter = event.submitter;
		message.textContent = confirmation;
		accept.textContent = event.submitter?.dataset.confirmLabel || form.dataset.confirmLabel || '确认继续';
		dialog.showModal();
	  });
	});
	accept?.addEventListener('click', () => {
	  const form = pendingForm;
	  const submitter = pendingSubmitter;
	  pendingForm = null;
	  pendingSubmitter = null;
	  dialog?.close();
	  if (!form) return;
	  form.dataset.confirmed = '1';
	  form.requestSubmit(submitter || undefined);
	});
	cancel?.addEventListener('click', () => dialog?.close());
	dialog?.addEventListener('close', () => {
	  pendingForm = null;
	  pendingSubmitter = null;
	});
  };

  const bindTaskPage = () => {
    const page = document.querySelector('[data-task-page]');
    if (!page) return;
    const filters = page.querySelector('.task-filter-panel');
    if (filters && window.matchMedia('(max-width: 820px)').matches) filters.open = false;
    const rows = Array.from(document.querySelectorAll('[data-task-status]'));
    const requested = new URLSearchParams(window.location.search).get('task');
    const targetID = window.location.hash.startsWith('#task-') ? window.location.hash.slice(1) : (requested ? 'task-' + requested : '');
    const target = targetID ? document.getElementById(targetID) : null;
	const loadMore = page.querySelector('[data-task-load-more]');
	const loadMoreButton = page.querySelector('[data-task-load-more-button]');
	const visibleCount = page.querySelector('[data-task-visible-count]');
	let visibleRows = rows.length;
	const applyVisibleRows = () => {
	  rows.forEach((row, index) => { row.hidden = index >= visibleRows; });
	  if (visibleCount) visibleCount.textContent = String(Math.min(visibleRows, rows.length));
	  if (loadMore) loadMore.hidden = visibleRows >= rows.length;
	};
	if (window.matchMedia('(max-width: 820px)').matches && rows.length > 20) {
	  const targetIndex = target ? rows.indexOf(target) : -1;
	  visibleRows = Math.min(rows.length, Math.max(20, targetIndex + 1));
	  applyVisibleRows();
	  loadMoreButton?.addEventListener('click', () => {
		visibleRows = Math.min(rows.length, visibleRows + 20);
		applyVisibleRows();
		if (loadMore?.hidden) rows[rows.length - 1]?.scrollIntoView({block: 'nearest'});
	  });
	}
    if (target) {
      target.querySelector('details')?.setAttribute('open', '');
      window.requestAnimationFrame(() => target.scrollIntoView({block: 'center'}));
    }
    const status = page.querySelector('[data-task-refresh-status]');
    const badge = document.querySelector('[data-task-active-count]');
    const terminal = new Set(['succeeded', 'failed', 'canceled']);
    const labels = {pending: '准备中', running: '执行中', succeeded: '成功', failed: '失败', canceled: '已取消'};
    const classes = {pending: 'warn', running: 'warn', succeeded: 'ok', failed: 'bad', canceled: 'muted'};
    let timer = 0;
    let polling = false;
	const pollInterval = Math.max(600, Math.min(5000, Number(page.dataset.taskPollMs) || 600));

    const activeRows = () => rows.filter((row) => row.dataset.taskStatus === 'pending' || row.dataset.taskStatus === 'running');
    const setStatus = (message, className = '') => {
      if (!status) return;
      status.textContent = message;
      status.classList.toggle('syncing', className === 'syncing');
      status.classList.toggle('poll-error', className === 'poll-error');
    };
    const updateBadge = (active) => {
      if (!badge) return;
      badge.textContent = String(active);
      badge.hidden = active === 0;
    };
    const resultLink = (taskID, hasResult) => {
      const destination = new URL(window.location.href);
      destination.searchParams.set('task', taskID);
      destination.hash = 'task-' + taskID;
      const link = document.createElement('a');
      link.className = 'task-result-refresh';
      link.href = destination.pathname + destination.search + destination.hash;
      link.textContent = hasResult ? '查看完整结果 →' : '刷新执行记录 →';
      link.addEventListener('click', (event) => {
        if (link.href !== window.location.href) return;
        event.preventDefault();
        window.location.reload();
      });
      return link;
    };
    const updateRow = (row, payload) => {
      if (!payload || payload.id !== row.dataset.taskId) throw new Error('invalid task status payload');
      row.dataset.taskStatus = payload.status;
      row.setAttribute('aria-busy', terminal.has(payload.status) ? 'false' : 'true');
      const label = row.querySelector('[data-live-task-label]');
      const marker = row.querySelector('.timeline-marker>i');
      const attempt = row.querySelector('[data-live-task-attempt]');
      const timing = row.querySelector('[data-live-task-timing]');
      const pendingAction = row.querySelector('[data-live-pending-action]');
      const result = row.querySelector('[data-live-task-result]');
	  const simulated = payload.status === 'succeeded' && payload.simulated === true;
	  row.dataset.taskSimulated = simulated ? '1' : '0';
	  const statusClass = simulated ? 'warn' : (classes[payload.status] || 'muted');
      [label, marker].forEach((element) => {
        if (!element) return;
        element.classList.remove('ok', 'warn', 'bad', 'muted');
        element.classList.add(statusClass);
      });
	  if (label) label.textContent = simulated ? '模拟完成' : (labels[payload.status] || payload.status);
      if (attempt) attempt.textContent = payload.attempt ? '第 ' + payload.attempt + ' 次执行' : '尚未开始';
      if (timing && payload.timing) timing.textContent = payload.timing;
      if (pendingAction) pendingAction.hidden = payload.status !== 'pending';
      if (result) {
        result.replaceChildren();
        if (terminal.has(payload.status)) result.appendChild(resultLink(payload.id, Boolean(payload.has_result)));
        else {
          const message = document.createElement('span');
          message.textContent = payload.status === 'pending' ? '准备中' : '执行中';
          result.appendChild(message);
        }
      }
      updateBadge(Number(payload.tasks_active) || 0);
    };
    const schedule = () => {
      window.clearTimeout(timer);
      timer = window.setTimeout(poll, pollInterval);
    };
    const poll = async () => {
      if (polling || document.hidden) return;
      const active = activeRows();
      if (active.length === 0) {
        setStatus('已同步');
        return;
      }
      polling = true;
      setStatus('同步中', 'syncing');
      let failed = false;
      await Promise.all(active.map(async (row) => {
        try {
          const response = await fetch('/ui/tasks/' + encodeURIComponent(row.dataset.taskId) + '/status', {credentials: 'same-origin', cache: 'no-store', headers: {'Accept': 'application/json'}});
          if (response.status === 401) {
            window.location.assign('/login?error=' + encodeURIComponent('会话已过期，请重新登录'));
            return;
          }
          if (!response.ok) throw new Error('HTTP ' + response.status);
          updateRow(row, await response.json());
        } catch (_) {
          failed = true;
        }
      }));
      polling = false;
      if (failed) setStatus('同步失败，请刷新', 'poll-error');
      else if (activeRows().length === 0) setStatus('已更新');
      if (activeRows().length > 0) schedule();
    };
    document.addEventListener('visibilitychange', () => {
      window.clearTimeout(timer);
      if (!document.hidden) poll();
    });
    poll();
  };

  const bindSecretCopy = () => {
	const fallbackCopy = (value) => {
	  const input = document.createElement('textarea');
	  input.value = value;
	  input.setAttribute('readonly', '');
	  input.style.position = 'fixed';
	  input.style.opacity = '0';
	  document.body.appendChild(input);
	  input.select();
	  let copied = false;
	  try { copied = document.execCommand('copy'); } catch (_) {}
	  input.remove();
	  return copied;
	};
	const copyText = async (value) => {
	  if (navigator.clipboard?.writeText) await navigator.clipboard.writeText(value);
	  else if (!fallbackCopy(value)) throw new Error('clipboard unavailable');
	};
    document.querySelectorAll('[data-copy-secret]').forEach((button) => {
      button.addEventListener('click', async () => {
        const secret = button.closest('.access-secret')?.querySelector('code')?.textContent?.trim();
		if (!secret) return;
        try {
		  await copyText(secret);
          button.textContent = '已复制';
        } catch (_) {
		  const selection = window.getSelection();
		  const range = document.createRange();
		  const code = button.closest('.access-secret')?.querySelector('code');
		  if (selection && code) { range.selectNodeContents(code); selection.removeAllRanges(); selection.addRange(range); }
		  button.textContent = '请手动复制';
        }
      });
    });
	document.querySelectorAll('[data-copy-value]').forEach((button) => {
	  button.addEventListener('click', async () => {
		const selector = button.dataset.copyTarget;
		if (!selector) return;
		const target = document.querySelector(selector);
		const value = target?.value || target?.textContent?.trim();
		if (!value) return;
		try {
		  await copyText(value);
		  button.textContent = '已复制';
		} catch (_) {
		  if (target?.select) target.select();
		  button.textContent = '请手动复制';
		}
	  });
	});
  };

  const bindSecretVisibility = () => {
	document.querySelectorAll('[data-secret-visibility]').forEach((button) => {
	  button.addEventListener('click', () => {
		const input = button.closest('.secret-value-control')?.querySelector('input');
		if (!input) return;
		const reveal = input.type === 'password';
		input.type = reveal ? 'text' : 'password';
		button.textContent = reveal ? '隐藏' : '显示';
		button.setAttribute('aria-label', (reveal ? '隐藏' : '显示') + (button.dataset.secretLabel || '凭据'));
	  });
	});
  };

  const bindConfigurationDrafts = () => {
    const prefix = 'qcontrolhub-config-draft:' + window.location.pathname + ':';
    const forms = Array.from(document.querySelectorAll('form[action$="/save"]'));
    const keyFor = (form) => {
      const mode = form.querySelector('input[name="mode"]')?.value || 'profile';
      const subject = form.querySelector('input[name="protocol"]')?.value || form.querySelector('input[name="key"]')?.value || '';
      return prefix + mode + ':' + subject;
    };
    const search = new URLSearchParams(window.location.search);
    if (search.has('notice') || search.has('source_task')) {
      try {
        Object.keys(sessionStorage).filter((key) => key.startsWith(prefix)).forEach((key) => sessionStorage.removeItem(key));
      } catch (_) {}
    }
	if (!search.has('notice') && !search.has('source_task')) {
      forms.forEach((form) => {
        let values;
        try { values = JSON.parse(sessionStorage.getItem(keyFor(form)) || '[]'); } catch (_) { values = []; }
        if (!Array.isArray(values) || values.length === 0) return;
        values.forEach((saved) => {
          const control = Array.from(form.elements).find((element) => element.name === saved.name && element.type !== 'hidden');
          if (!control) return;
          if (control.type === 'checkbox' || control.type === 'radio') control.checked = Boolean(saved.checked);
          else control.value = saved.value;
        });
        form.querySelectorAll('[data-code-input]').forEach((control) => control.dispatchEvent(new Event('input', {bubbles: true})));
        form.querySelector('select[name="engine"]')?.dispatchEvent(new Event('change', {bubbles: true}));
        form.closest('.advanced-config, .advanced-studio')?.setAttribute('open', '');
        form.closest('.source-editor, .source-studio')?.setAttribute('open', '');
      });
    }
    forms.forEach((form) => {
	  const persist = () => {
        const values = Array.from(form.elements)
          .filter((control) => control.name && control.type !== 'hidden' && control.type !== 'submit' && control.type !== 'button')
          .map((control) => ({name: control.name, value: control.value, checked: control.checked}));
        try { sessionStorage.setItem(keyFor(form), JSON.stringify(values)); } catch (_) {}
	  };
	  form.addEventListener('input', persist);
	  form.addEventListener('change', persist);
	  form.addEventListener('submit', persist);
    });
	document.querySelectorAll('.server-generate-actions a[href*="regenerate=1"], .builder-actions a[href*="regenerate=1"]').forEach((link) => {
	  const form = link.closest('form.server-form');
	  if (!form) return;
	  link.addEventListener('click', () => {
		try { sessionStorage.removeItem(keyFor(form)); } catch (_) {}
	  });
	});
  };

  const bindSubmitOnce = () => {
	document.querySelectorAll('form').forEach((form) => {
	  form.addEventListener('submit', (event) => {
		if (event.defaultPrevented) return;
		if (form.dataset.submitting === '1') {
		  event.preventDefault();
		  return;
		}
		form.dataset.submitting = '1';
		window.setTimeout(() => {
		  form.querySelectorAll('button[type="submit"], input[type="submit"]').forEach((control) => { control.disabled = true; });
		}, 0);
	  });
	});
  };

  const bindAutomaticCurrentConfig = () => {
	const form = document.querySelector('[data-auto-read-current]');
	if (!form) return;
	window.requestAnimationFrame(() => form.requestSubmit());
  };

  const bindBuilderMenus = () => {
    document.querySelectorAll('[data-builder-workbench]').forEach((workbench) => {
      const form = workbench.closest('form');
      const links = Array.from(workbench.querySelectorAll('[data-builder-step]'));
      const sections = Array.from(workbench.querySelectorAll('.builder-section[id]'));
      if (!links.length || !sections.length) return;
      const activate = (id) => {
        const selected = sections.find((section) => section.id === id) || sections[0];
        sections.forEach((section) => {
          const active = section === selected;
          section.hidden = !active;
          section.setAttribute('role', 'tabpanel');
          section.setAttribute('aria-hidden', active ? 'false' : 'true');
        });
        links.forEach((link, index) => {
          const active = link.dataset.builderStep === selected.id;
          link.classList.toggle('active', active);
          link.setAttribute('role', 'tab');
          link.setAttribute('aria-selected', active ? 'true' : 'false');
          link.tabIndex = active ? 0 : -1;
          if (!link.id) link.id = 'builder-step-' + index;
          if (active) selected.setAttribute('aria-labelledby', link.id);
        });
        workbench.dataset.builderActive = selected.id;
        return selected;
      };
      links.forEach((link) => {
        link.addEventListener('click', (event) => {
          event.preventDefault();
          activate(link.dataset.builderStep);
        });
        link.addEventListener('keydown', (event) => {
          if (!['ArrowDown', 'ArrowRight', 'ArrowUp', 'ArrowLeft', 'Home', 'End'].includes(event.key)) return;
          event.preventDefault();
          const current = links.indexOf(link);
          const next = event.key === 'Home' ? 0 : event.key === 'End' ? links.length - 1 :
            (current + (event.key === 'ArrowDown' || event.key === 'ArrowRight' ? 1 : -1) + links.length) % links.length;
          activate(links[next].dataset.builderStep);
          links[next].focus();
        });
      });
      form?.addEventListener('invalid', (event) => {
        const section = event.target.closest?.('.builder-section');
        if (!section || !section.hidden) return;
        activate(section.id);
        window.requestAnimationFrame(() => event.target.focus());
      }, true);
      activate(location.hash && sections.some((section) => '#' + section.id === location.hash) ? location.hash.slice(1) : sections[0].id);
    });
  };

  const bindCodeEditors = () => {
    document.querySelectorAll('[data-code-editor]').forEach((editor) => {
      const input = editor.querySelector('[data-code-input]');
      const gutter = editor.querySelector('[data-line-numbers]');
      const bytes = editor.querySelector('[data-code-bytes]');
      const position = editor.querySelector('[data-code-position]');
      const status = editor.querySelector('[data-code-status]');
      const statusDot = editor.querySelector('[data-code-status-dot]');
      const validation = editor.querySelector('[data-code-validation]');
      const reset = editor.querySelector('[data-code-reset]');
      if (!input || !gutter) return;
      const form = input.closest('form');
      const maxBytes = Number(editor.dataset.codeMaxBytes) || 2 * 1024 * 1024;
      let original = input.value;
      let baselineStatus = status?.textContent || '已保存';
      let baselineValidation = validation?.textContent || '';
      input.setAttribute('wrap', 'off');
      const formatBytes = (value) => {
        if (value < 1024) return value + ' B';
        if (value < 1024 * 1024) return (value / 1024).toFixed(1) + ' KiB';
        return (value / (1024 * 1024)).toFixed(2) + ' MiB';
      };
      const updatePosition = () => {
        if (!position) return;
        const before = input.value.slice(0, input.selectionStart);
        const lineStart = before.lastIndexOf('\n') + 1;
        const line = before.split('\n').length;
        position.textContent = '行 ' + line + '，列 ' + (before.length - lineStart + 1);
      };
      const blockSubmit = (blocked) => {
        form?.querySelectorAll('button[type="submit"], input[type="submit"]').forEach((control) => {
          if (blocked && !control.disabled) {
            control.disabled = true;
            control.dataset.codeBlocked = '1';
          } else if (!blocked && control.dataset.codeBlocked === '1') {
            control.disabled = false;
            delete control.dataset.codeBlocked;
          }
        });
      };
      const checkContent = (size) => {
        if (size > maxBytes) return {valid: false, status: '内容过大', message: '配置源码超过 2 MiB 上限，无法提交。'};
        if (!input.value.trim()) return {valid: false, status: '内容为空', message: '配置源码不能为空。'};
        if ((editor.dataset.codeLanguage || '').toUpperCase() !== 'JSON') return {valid: true};
        try {
          JSON.parse(input.value);
          return {valid: true, json: true};
        } catch (error) {
          const reason = String(error?.message || '');
          const location = reason.match(/line\s+(\d+)\s+column\s+(\d+)/i);
          const message = location
            ? 'JSON 第 ' + location[1] + ' 行、第 ' + location[2] + ' 列附近存在语法错误。'
            : 'JSON 语法错误，请检查括号、逗号和引号。';
          return {valid: false, status: '语法错误', message};
        }
      };
      const update = () => {
        const lines = Math.max(1, input.value.split('\n').length);
        const size = new Blob([input.value]).size;
        const result = checkContent(size);
        const dirty = input.value !== original;
        gutter.textContent = Array.from({length: lines}, (_, index) => String(index + 1)).join('\n');
        if (bytes) bytes.textContent = formatBytes(size) + (size > maxBytes ? ' / 2 MiB' : '');
        editor.dataset.dirty = dirty ? '1' : '0';
        editor.dataset.codeValid = result.valid ? '1' : '0';
        input.classList.toggle('is-invalid', !result.valid);
        if (reset) reset.disabled = !dirty;
        if (!result.valid) {
          if (status) status.textContent = result.status;
          if (validation) validation.textContent = result.message;
          if (statusDot) statusDot.style.background = 'var(--red)';
        } else if (dirty) {
          if (status) status.textContent = '未保存';
          if (validation) validation.textContent = result.json ? 'JSON 语法有效；提交后仍会由节点内核校验。' : baselineValidation;
          if (statusDot) statusDot.style.background = 'var(--amber)';
        } else {
          if (status) status.textContent = baselineStatus;
          if (validation) validation.textContent = baselineValidation;
          if (statusDot) statusDot.style.background = 'var(--green)';
        }
        blockSubmit(!result.valid);
        updatePosition();
      };
      input.addEventListener('input', update);
      input.addEventListener('scroll', () => { gutter.scrollTop = input.scrollTop; });
      ['click', 'keyup', 'select'].forEach((name) => input.addEventListener(name, updatePosition));
      input.addEventListener('keydown', (event) => {
        if (event.key === 'Tab' && !event.altKey && !event.ctrlKey && !event.metaKey) {
          event.preventDefault();
          const value = input.value;
          const start = input.selectionStart;
          const end = input.selectionEnd;
          const lineStart = value.lastIndexOf('\n', Math.max(0, start - 1)) + 1;
          const nextBreak = value.indexOf('\n', end);
          const lineEnd = nextBreak === -1 ? value.length : nextBreak;
          if (!event.shiftKey && start === end) {
            input.setRangeText('  ', start, end, 'end');
          } else {
            const block = value.slice(lineStart, lineEnd);
            const lines = block.split('\n');
            let removed = 0;
            const replacement = lines.map((line) => {
              if (!event.shiftKey) return '  ' + line;
              const match = line.match(/^(?: {1,2}|\t)/);
              if (!match) return line;
              removed += match[0].length;
              return line.slice(match[0].length);
            }).join('\n');
            input.setRangeText(replacement, lineStart, lineEnd, 'start');
            if (event.shiftKey) {
              input.setSelectionRange(Math.max(lineStart, start - Math.min(2, start - lineStart)), Math.max(lineStart, end - removed));
            } else {
              input.setSelectionRange(start + 2, end + lines.length * 2);
            }
          }
          input.dispatchEvent(new Event('input', {bubbles: true}));
          return;
        }
        if (event.key === 'Enter' && !event.altKey && !event.ctrlKey && !event.metaKey && input.selectionStart === input.selectionEnd) {
          const start = input.selectionStart;
          const lineStart = input.value.lastIndexOf('\n', Math.max(0, start - 1)) + 1;
          const before = input.value.slice(lineStart, start);
          const indentation = before.match(/^\s*/)?.[0] || '';
          const extra = /[:[{]\s*$/.test(before) ? '  ' : '';
          if (indentation || extra) {
            event.preventDefault();
            input.setRangeText('\n' + indentation + extra, start, start, 'end');
            input.dispatchEvent(new Event('input', {bubbles: true}));
          }
        }
      });
      input.addEventListener('code-editor-baseline', (event) => {
        original = typeof event.detail?.value === 'string' ? event.detail.value : input.value;
        baselineStatus = event.detail?.status || baselineStatus;
        baselineValidation = event.detail?.validation || baselineValidation;
        update();
      });
      reset?.addEventListener('click', () => {
        input.value = original;
        input.setSelectionRange(0, 0);
        update();
        input.focus();
      });
      form?.addEventListener('submit', (event) => {
        const result = checkContent(new Blob([input.value]).size);
        if (!result.valid) {
          event.preventDefault();
          update();
          input.focus();
          return;
        }
        editor.dataset.dirty = '0';
      }, {capture: true});
      update();
    });
    window.addEventListener('beforeunload', (event) => {
      if (!document.querySelector('[data-code-editor][data-dirty="1"]')) return;
      event.preventDefault();
      event.returnValue = '';
    });
  };

  const bindTaskFeedback = () => {
	const feedback = document.querySelector('[data-task-feedback]');
	if (!feedback) return;
	const taskID = feedback.dataset.taskFeedback;
	const message = feedback.querySelector('[data-task-feedback-message]');
	const link = feedback.querySelector('[data-task-feedback-link]');
	const badge = document.querySelector('[data-task-active-count]');
	if (!taskID || !message || !link) return;
	const actions = {validate: '校验配置', deploy: '部署并重启', start: '启动服务', stop: '停止服务', restart: '重启服务', status: '查询状态', install: '安装或切换内核', 'read-config': '读取当前配置'};
	const engines = {mihomo: 'Mihomo', xray: 'Xray', 'sing-box': 'sing-box', 'ss-rust': 'Shadowsocks Rust'};
	const terminal = new Set(['succeeded', 'failed', 'canceled']);
	let stopped = false;
	const update = (payload) => {
	  if (!payload || payload.id !== taskID) throw new Error('invalid task status payload');
	  const active = Number(payload.tasks_active) || 0;
	  if (badge) {
		badge.textContent = String(active);
		badge.hidden = active === 0;
	  }
	  const subject = (engines[payload.engine] || payload.engine || '节点') + ' ' + (actions[payload.action] || payload.action || '任务');
	  const openingCurrent = payload.action === 'read-config' && feedback.dataset.autoLoadSource === '1';
	  const labels = {
		pending: openingCurrent ? '正在打开当前配置' : subject + '：准备执行',
		running: openingCurrent ? '正在打开当前配置' : subject + '：正在执行',
		succeeded: subject + (payload.simulated === true ? '：模拟完成' : '：已完成'),
		failed: subject + '：执行失败',
		canceled: subject + '：已取消'
	  };
	  feedback.classList.remove('pending', 'running', 'succeeded', 'failed', 'canceled');
	  feedback.classList.add(payload.status);
	  feedback.dataset.pollError = '0';
	  feedback.setAttribute('aria-busy', terminal.has(payload.status) ? 'false' : 'true');
	  message.textContent = labels[payload.status] || subject + '状态：' + payload.status;
	  link.textContent = terminal.has(payload.status) ? '查看执行结果 →' : '查看任务详情 →';
	  link.hidden = openingCurrent && payload.status !== 'failed' && payload.status !== 'canceled';
	  if (payload.action === 'read-config' && payload.status === 'succeeded' && (window.location.pathname.includes('/config/') || window.location.pathname === '/configs')) {
		const destination = new URL(window.location.href);
		destination.searchParams.delete('notice');
		destination.searchParams.delete('error');
		destination.searchParams.delete('task');
		destination.searchParams.set('source_task', taskID);
		destination.hash = window.location.pathname === '/configs' ? 'config-editor' : '';
		link.href = destination.pathname + destination.search + destination.hash;
		link.textContent = '打开当前配置 →';
		if (feedback.dataset.autoLoadSource === '1' && (window.location.pathname.includes('/config/') || window.location.pathname === '/configs')) {
		  stopped = true;
		  message.textContent = (engines[payload.engine] || payload.engine) + ' 当前配置已校验，正在打开编辑器…';
		  window.location.replace(link.href);
		  return;
		}
	  }
	  stopped = terminal.has(payload.status);
	};
	const poll = async () => {
	  if (stopped) return;
	  if (document.hidden) {
		window.setTimeout(poll, 1500);
		return;
	  }
	  try {
		const response = await fetch('/ui/tasks/' + encodeURIComponent(taskID) + '/status', {credentials: 'same-origin', cache: 'no-store', headers: {'Accept': 'application/json'}});
		if (response.status === 401) {
		  window.location.assign('/login?error=' + encodeURIComponent('会话已过期，请重新登录'));
		  return;
		}
		if (!response.ok) throw new Error('HTTP ' + response.status);
		update(await response.json());
	  } catch (_) {
		feedback.dataset.pollError = '1';
	  }
	  if (!stopped) window.setTimeout(poll, 200);
	};
	window.setTimeout(poll, 60);
  };

  document.addEventListener('DOMContentLoaded', () => {
    openAdvancedFromHash();
    bindCodeEditors();
    bindProfileEditor();
    bindConfigurationDrafts();
    bindServerForm();
    bindBuilderMenus();
    bindCoreVersionForms();
    bindConfirmations();
    bindSubmitOnce();
	bindAutomaticCurrentConfig();
	bindTaskPage();
	bindTaskFeedback();
    bindSecretCopy();
	bindSecretVisibility();
  });
  window.addEventListener('hashchange', openAdvancedFromHash);
})();
`

func (s *Server) agentConfigScript(w http.ResponseWriter, r *http.Request) {
	s.serveAsset(w, r, "text/javascript; charset=utf-8", s.agentConfigJSAsset)
}

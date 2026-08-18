package webui

import "net/http"

const nodeWorkspaceScript = `
(() => {
  const nodePrefix = 'node-';

  document.addEventListener('DOMContentLoaded', () => {
    const enrollment = document.querySelector('[data-enrollment-panel]');
    const revealEnrollment = () => {
      if (!enrollment) return;
      enrollment.open = true;
      window.requestAnimationFrame(() => enrollment.scrollIntoView({behavior: 'smooth', block: 'start'}));
    };
    document.querySelectorAll('a[href="#enrollment"]').forEach((link) => {
      link.addEventListener('click', (event) => {
        event.preventDefault();
        if (window.location.hash !== '#enrollment') window.history.pushState(window.history.state, '', '#enrollment');
        revealEnrollment();
      });
    });
    window.addEventListener('hashchange', () => {
      if (window.location.hash === '#enrollment') revealEnrollment();
    });
    if (window.location.hash === '#enrollment') revealEnrollment();

    document.querySelectorAll('details.machine-workspace[name="node-workspace"]').forEach((node) => {
      node.addEventListener('toggle', () => {
        if (!node.open || !node.id.startsWith(nodePrefix)) return;
        const selected = node.id.slice(nodePrefix.length);
        if (!selected) return;
        const url = new URL(window.location.href);
        if (url.searchParams.get('node') === selected) return;
        url.searchParams.set('node', selected);
        window.history.replaceState(window.history.state, '', url.pathname + url.search + url.hash);
      });
    });

    document.querySelectorAll('.core-version-form').forEach((form) => {
      const channels = Array.from(form.querySelectorAll('input[name="release_channel"]'));
      const version = form.querySelector('input[name="custom_version"]');
      if (!channels.length || !version) return;
      const apply = () => {
        const custom = channels.find((channel) => channel.checked)?.value === 'custom';
        version.disabled = !custom;
        version.required = custom;
        version.closest('label')?.classList.toggle('is-disabled', !custom);
      };
      channels.forEach((channel) => channel.addEventListener('change', apply));
      apply();
    });

    document.querySelectorAll('[data-open-version-form]').forEach((trigger) => {
      trigger.addEventListener('click', () => {
        const drawer = trigger.closest('.service-card')?.querySelector('.version-drawer');
        const form = drawer?.querySelector('.core-version-form');
        if (!drawer || !form) return;
        drawer.open = true;
        window.requestAnimationFrame(() => {
          form.scrollIntoView({behavior: 'smooth', block: 'nearest'});
          form.querySelector('input[name="release_channel"]:checked')?.focus({preventScroll: true});
        });
      });
    });

    const batchForm = document.querySelector('[data-batch-form]');
    if (batchForm) {
      const refreshBatch = () => {
        const count = document.querySelectorAll('[data-batch-checkbox]:checked').length;
        batchForm.querySelectorAll('[data-batch-submit]').forEach((button) => { button.disabled = count === 0; });
        batchForm.querySelectorAll('[data-batch-count]').forEach((label) => {
          label.textContent = count ? '已选择 ' + count + ' 个节点' : '未选择节点';
        });
      };
      document.querySelectorAll('[data-batch-checkbox]').forEach((checkbox) => {
        checkbox.addEventListener('change', refreshBatch);
        // The checkbox lives inside the machine-header <summary>; without this,
        // clicking it also toggles the node workspace open/closed.
        checkbox.addEventListener('click', (event) => event.stopPropagation());
      });
      batchForm.addEventListener('submit', (event) => {
        const ids = Array.from(document.querySelectorAll('[data-batch-checkbox]:checked')).map((box) => box.value);
        if (!ids.length) { event.preventDefault(); return; }
        const hidden = document.createElement('input');
        hidden.type = 'hidden';
        hidden.name = 'agent_ids';
        hidden.value = ids.join(',');
        batchForm.appendChild(hidden);
      });
      refreshBatch();
    }
  });
})();
`

func (s *Server) nodeWorkspaceScript(w http.ResponseWriter, r *http.Request) {
	s.serveAsset(w, r, "text/javascript; charset=utf-8", s.nodeWorkspaceJSAsset)
}

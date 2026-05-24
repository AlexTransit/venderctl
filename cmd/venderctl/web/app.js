const _params = new URLSearchParams(location.search);
if (_params.get('open') === 'admin-messages') {
    window.__openAdminMessages = true;
}
const _openUserId = parseInt(_params.get('open_user') || '0');
const _openUserType = parseInt(_params.get('open_user_type') || '0');
if (_openUserId > 0) window.__openUserMessages = { id: _openUserId, type: _openUserType };

setTimeout(() => {
    const splash = document.getElementById('splash');
    splash.style.transition = 'opacity 0.5s';
    splash.style.opacity = '0';
    setTimeout(() => splash.remove(), 500);
}, 1000);

let currentDefaultVmid = null;
let pendingOrder = null;
let ordersOffset = 0;
let adminMessagesOffset = 0;
let isAdminUser = false;
let currentAdminReplyId = 0;
let currentAdminMessageId = 0;
let canInstallPWA = false;
let hideAnswered = false;
let deferredInstallPrompt = null;
let swRegistrationPromise = null;
const basePath = location.pathname === '/' ? '' : location.pathname.replace(/\/+$/, '');

function withBase(path) {
    return `${basePath}${path}`;
}

// === SERVICE WORKER ===

function registerServiceWorker() {
    if (!('serviceWorker' in navigator)) return;
    swRegistrationPromise = new Promise((resolve) => {
        window.addEventListener('load', () => {
            navigator.serviceWorker.register(withBase('/sw.js'), { scope: withBase('/'), updateViaCache: 'none' })
                .then(resolve)
                .catch((err) => {
                    console.log('SW registration failed:', err);
                    resolve(null);
                });
        });
    });
}

registerServiceWorker();

if ('serviceWorker' in navigator) {
    navigator.serviceWorker.addEventListener('message', (event) => {
        // notification_click приходит напрямую (не в payload)
        if (event.data && event.data.type === 'notification_click' && event.data.sender_id) {
            handleOpenUserMessages(event.data.sender_id, event.data.sender_type);
            return;
        }
        const data = event.data && event.data.payload ? event.data.payload : {};
        if (data.kind === 'admin_reply' && data.message_id && data.reply) {
            showAdminReplyModal(data.message_id, data.message || '', data.reply);
        }
        if (data.kind === 'admin_message' && data.message_id && data.message) {
            showAdminMessageModal(data.message_id, data.message);
        }
        // юзер ответил на сообщение админа — перезагрузить список с показом ответов
        if (data.kind === 'user_reply') {
            document.getElementById('admin-hide-answered').checked = true;
            hideAnswered = true;
            adminMessagesOffset = 0;
            document.getElementById('admin-messages-list').innerHTML = '';
            const card = document.getElementById('admin-messages-card');
            if (card && card.style.display !== 'none') {
                loadMoreAdminMessages();
            } else {
                openAdminMessages();
            }
        }
        // push когда пользователь написал админу (нет kind, есть sender_id)
        if (!data.kind && data.sender_id) {
            handleOpenUserMessages(data.sender_id, data.sender_type || 0);
        }
    });
}

// === PWA / STANDALONE ===

function isStandaloneApp() {
    if (window.matchMedia('(display-mode: standalone)').matches) return true;
    if (window.matchMedia('(display-mode: fullscreen)').matches) return true;
    if (window.matchMedia('(display-mode: minimal-ui)').matches) return true;
    if (window.navigator.standalone === true) return true;
    return document.referrer.startsWith('android-app://');
}

function applyStandaloneUI() {
    const standalone = isStandaloneApp();
    const installItem = document.getElementById('install-app-item');
    if (installItem) installItem.style.display = (!standalone && canInstallPWA) ? 'block' : 'none';
}

const dmStandalone = window.matchMedia('(display-mode: standalone)');
const dmFullscreen = window.matchMedia('(display-mode: fullscreen)');
const dmMinimalUI = window.matchMedia('(display-mode: minimal-ui)');
dmStandalone.addEventListener('change', applyStandaloneUI);
dmFullscreen.addEventListener('change', applyStandaloneUI);
dmMinimalUI.addEventListener('change', applyStandaloneUI);
window.addEventListener('pageshow', applyStandaloneUI);
applyStandaloneUI();

window.addEventListener('beforeinstallprompt', (e) => {
    e.preventDefault();
    deferredInstallPrompt = e;
    canInstallPWA = true;
    applyStandaloneUI();
});

window.addEventListener('appinstalled', () => {
    deferredInstallPrompt = null;
    canInstallPWA = false;
    applyStandaloneUI();
});

async function installPWA() {
    toggleMenu();
    if (!deferredInstallPrompt) {
        alert('Установка сейчас недоступна.\nОткройте страницу в Chrome (не внутри Telegram), по HTTPS, обновите страницу и попробуйте снова.');
        return;
    }
    deferredInstallPrompt.prompt();
    await deferredInstallPrompt.userChoice;
    deferredInstallPrompt = null;
    const item = document.getElementById('install-app-item');
    if (item) item.style.display = 'none';
}

function isIOSDevice() {
    const ua = navigator.userAgent || '';
    const isIOS = /iPad|iPhone|iPod/.test(ua);
    const isIPadOS = (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1);
    return isIOS || isIPadOS;
}

function showIOSInstallHelp() {
    toggleMenu();
    document.getElementById('ios-install-modal').style.display = 'block';
}

// === МЕНЮ ===

function toggleMenu() {
    const menu = document.getElementById('side-menu');
    const overlay = document.getElementById('menu-overlay');
    menu.classList.toggle('active');
    overlay.style.display = menu.classList.contains('active') ? 'block' : 'none';
}

// === ЗАКАЗЫ (ИСТОРИЯ) ===

function openOrders() {
    toggleMenu();
    ordersOffset = 0;
    document.getElementById('orders-list').innerHTML = '';
    document.getElementById('orders-card').style.display = 'block';
    document.getElementById('order-card').style.display = 'none';
    document.getElementById('admin-messages-card').style.display = 'none';
    loadMoreOrders();
}

function closeOrders() {
    document.getElementById('orders-card').style.display = 'none';
    document.getElementById('order-card').style.display = 'block';
}

function loadMoreOrders() {
    fetch(withBase(`/api/orders?offset=${ordersOffset}`))
        .then(res => res.json())
        .then(orders => {
            const list = document.getElementById('orders-list');
            if (orders.length === 0 && ordersOffset === 0) {
                list.innerHTML = '<p style="color:#888; text-align:center;">Заказов пока нет</p>';
                document.getElementById('orders-more').style.display = 'none';
                return;
            }
            orders.forEach(o => {
                const date = new Date(o.date).toLocaleString('ru-RU');
                const div = document.createElement('div');
                div.style = 'padding:10px; border-bottom:1px solid #eee; font-size:14px;';
                const balanceInfo = Number.isFinite(o.balance_info) ? o.balance_info.toFixed(2) : String(o.balance_info ?? '');
                div.innerHTML = `<span style="color:#888; font-size:12px;">${date}</span><br>${o.action} <span style="color:#666;">| баланс: ${balanceInfo}</span>`;
                list.appendChild(div);
            });
            ordersOffset += orders.length;
            document.getElementById('orders-more').style.display = orders.length === 10 ? 'block' : 'none';
        });
}

// === СООБЩЕНИЯ АДМИНА ===

function openAdminMessages() {
    document.getElementById('side-menu').classList.remove('active');
    document.getElementById('menu-overlay').style.display = 'none';
    if (!isAdminUser) {
        alert("Доступ только для администратора");
        return;
    }
    adminMessagesOffset = 0;
    hideAnswered = document.getElementById('admin-hide-answered').checked;
    document.getElementById('admin-messages-list').innerHTML = '';
    document.getElementById('admin-messages-card').style.display = 'block';
    document.getElementById('order-card').style.display = 'none';
    document.getElementById('orders-card').style.display = 'none';
    loadMoreAdminMessages();
}

function closeAdminMessages() {
    document.getElementById('admin-messages-card').style.display = 'none';
    document.getElementById('order-card').style.display = 'block';
}

function toggleHideAnswered() {
    adminMessagesOffset = 0;
    hideAnswered = document.getElementById('admin-hide-answered').checked;
    document.getElementById('admin-messages-list').innerHTML = '';
    loadMoreAdminMessages();
}

function loadMoreAdminMessages() {
    const hideParam = hideAnswered ? '&hide_answered=1' : '';
    fetch(withBase(`/api/admin/messages?offset=${adminMessagesOffset}${hideParam}`))
        .then(res => res.json())
        .then(messages => {
            const list = document.getElementById('admin-messages-list');
            if (messages.length === 0 && adminMessagesOffset === 0) {
                list.innerHTML = '<p style="color:#888; text-align:center;">Сообщений пока нет</p>';
                document.getElementById('admin-messages-more').style.display = 'none';
                return;
            }
            messages.forEach(m => {
                const div = document.createElement('div');
                div.style = 'padding:10px; border-bottom:1px solid #eee; font-size:14px;';
                const msgDate = new Date(m.created_at).toLocaleString('ru-RU');
                const replyDate = m.replied_at ? new Date(m.replied_at).toLocaleString('ru-RU') : '';
                const sender = m.from_admin ? 'Админ' : m.name;
                const receiver = m.from_admin ? m.name : 'Админ';
                const replyBlock = m.reply
                    ? `<div style="margin-top:6px; padding:6px 8px; background:#f0f7ff; border-radius:6px;">
                         <b>${receiver}</b><br>
                         <span style="color:#888; font-size:12px;">${replyDate}</span><br>
                         ${m.reply}
                       </div>`
                    : '';
                const replyBtn = (!m.from_admin && !m.reply)
                    ? `<br><button data-id="${m.id}" data-uid="${m.user_id}" data-utype="${m.user_type}" data-msg="${m.message.replace(/"/g, '&quot;').replace(/\n/g, ' ')}" onclick="replyAdminMessage(this)" style="margin-top:6px; padding:6px 10px; border:1px solid #0088cc; background:#fff; color:#0088cc; border-radius:8px; cursor:pointer;">Ответить</button>`
                    : '';
                div.innerHTML = `<b>${sender}</b><br>
                    <span style="color:#888; font-size:12px;">${msgDate}</span><br>
                    ${m.message}
                    ${replyBlock}${replyBtn}`;
                list.appendChild(div);
            });
            adminMessagesOffset += messages.length;
            document.getElementById('admin-messages-more').style.display = messages.length === 10 ? 'block' : 'none';
        });
}

function replyAdminMessage(btn) {
    const messageId = btn.dataset.id;
    const userMessage = btn.dataset.msg;
    const text = prompt("Ответ пользователю:", userMessage);
    if (text === null) return;
    const reply = text.trim();
    if (!reply) {
        alert("Пустой ответ");
        return;
    }
    fetch(withBase('/api/admin/reply'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            message_id: parseInt(messageId),
            user_id: parseInt(btn.dataset.uid),
            user_type: parseInt(btn.dataset.utype),
            reply
        })
    })
        .then(res => res.json().then(data => ({ ok: res.ok, data })))
        .then(({ ok, data }) => {
            if (!ok) {
                alert(data.error || "Не удалось отправить ответ");
                return;
            }
            adminMessagesOffset = 0;
            document.getElementById('admin-messages-list').innerHTML = '';
            loadMoreAdminMessages();
        })
        .catch(() => alert("Ошибка связи при отправке"));
}

// === НАПИСАТЬ ПОЛЬЗОВАТЕЛЮ ===

var allUsers = [];
var selectedSendUser = null;

function sendMessageToUser() {
    document.getElementById('side-menu').classList.remove('active');
    document.getElementById('menu-overlay').style.display = 'none';
    openSendToUserModal();
}

function openSendToUserModal() {
    const modal = document.getElementById('send-to-user-modal');
    modal.style.display = 'block';
    setTimeout(() => document.getElementById('send-to-user-filter').focus(), 100);
    document.getElementById('send-to-user-filter').value = '';
    document.getElementById('send-to-user-message').value = '';
    document.getElementById('send-to-user-selected').innerText = '';
    document.getElementById('send-to-user-result').innerText = '';
    selectedSendUser = null;
    document.getElementById('btn-rename-user').disabled = true;
    document.getElementById('btn-rename-user').style.color = '#888';
    document.getElementById('btn-rename-user').style.borderColor = '#ddd';
    document.getElementById('btn-invite-user').disabled = true;
    document.getElementById('btn-invite-user').style.color = '#888';
    document.getElementById('btn-invite-user').style.borderColor = '#ddd';
    document.getElementById('btn-memo-user').disabled = true;
    document.getElementById('btn-memo-user').style.color = '#888';
    document.getElementById('btn-memo-user').style.borderColor = '#ddd';

    if (allUsers.length === 0) {
        fetch(withBase('/api/admin/users'))
            .then(r => r.json())
            .then(data => {
                allUsers = data;
                renderUserList('');
            });
    } else {
        renderUserList('');
    }
}

function renderUserList(filter) {
    const list = document.getElementById('send-to-user-list');
    const f = filter.toLowerCase();
    const filtered = allUsers.filter(u => {
        const label = ((u.name || '') + ' ' + (u.memo || '') + ' ' + (u.phone || '') + ' ' + (u.user_id || '') + ' ' + (u.user_type || '')).toLowerCase();
        return !f || label.includes(f);
    });
    list.innerHTML = '';
    filtered.forEach(u => {
        const div = document.createElement('div');
        div.style = 'padding:10px 12px; cursor:pointer; border-bottom:1px solid #f0f0f0; font-size:15px;';
        div.innerText = (u.name || 'без имени') + (u.memo ? ' (' + u.memo + ')' : '') + ' [' + u.phone + ' ' + u.user_id + ':' + u.user_type + ']';
        div.onmouseenter = () => div.style.background = '#f0f7ff';
        div.onmouseleave = () => div.style.background = '';
        div.onclick = () => {
            selectedSendUser = u;
            document.getElementById('send-to-user-selected').innerText =
                '→ ' + (u.name || '') + (u.memo ? ' (' + u.memo + ')' : '') + ' [' + u.phone + ' ' + u.user_id + ':' + u.user_type + ']';
            document.getElementById('send-to-user-message').focus();
            ['btn-rename-user', 'btn-invite-user', 'btn-memo-user'].forEach(id => {
                const btn = document.getElementById(id);
                btn.disabled = false;
                btn.style.color = '#0088cc';
                btn.style.borderColor = '#0088cc';
            });
        };
        list.appendChild(div);
    });
    if (filtered.length === 0) {
        list.innerHTML = '<div style="padding:12px; color:#999; text-align:center;">Не найдено</div>';
    }
}

function doSendToUser() {
    if (!selectedSendUser) { alert('Выберите пользователя'); return; }
    const message = document.getElementById('send-to-user-message').value.trim();
    if (!message) { alert('Введите сообщение'); return; }

    fetch(withBase('/api/admin/send'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_id: selectedSendUser.user_id, user_type: selectedSendUser.user_type, message })
    })
        .then(r => r.json().then(d => ({ ok: r.ok, data: d })))
        .then(({ ok, data }) => {
            if (!ok) {
                document.getElementById('send-to-user-result').innerText = '❌ ' + (data.error || 'Ошибка');
                return;
            }
            document.getElementById('send-to-user-result').innerText = '✅ Отправлено';
            setTimeout(() => {
                document.getElementById('send-to-user-modal').style.display = 'none';
            }, 1000);
        })
        .catch(() => {
            document.getElementById('send-to-user-result').innerText = '❌ Ошибка связи';
        });
}

function renameUser() {
    if (!selectedSendUser) return;
    const current = selectedSendUser.name || '';
    const name = prompt('Новое имя:', current);
    if (name === null) return;
    const trimmed = name.trim();
    if (!trimmed) { alert('Имя не может быть пустым'); return; }
    fetch(withBase('/api/admin/rename-user'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_id: selectedSendUser.user_id, user_type: selectedSendUser.user_type, name: trimmed })
    })
        .then(r => r.json().then(d => ({ ok: r.ok, data: d })))
        .then(({ ok, data }) => {
            if (!ok) { alert(data.error || 'Ошибка'); return; }
            selectedSendUser.name = trimmed;
            allUsers = [];  // сбросить кэш списка
            document.getElementById('send-to-user-selected').innerText =
                '→ ' + trimmed + (selectedSendUser.memo ? ' (' + selectedSendUser.memo + ')' : '');
        })
        .catch(() => alert('Ошибка связи'));
}

function memoUser() {
    if (!selectedSendUser) return;
    const current = selectedSendUser.memo || '';
    const memo = prompt('Memo:', current);
    if (memo === null) return;
    fetch(withBase('/api/admin/memo-user'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_id: selectedSendUser.user_id, user_type: selectedSendUser.user_type, memo: memo.trim() })
    })
        .then(r => r.json().then(d => ({ ok: r.ok, data: d })))
        .then(({ ok, data }) => {
            if (!ok) { alert(data.error || 'Ошибка'); return; }
            selectedSendUser.memo = memo.trim();
            allUsers = [];
        })
        .catch(() => alert('Ошибка связи'));
}

function inviteUser() {
    if (!selectedSendUser) return;
    fetch(withBase('/api/admin/invite'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ user_id: selectedSendUser.user_id, user_type: selectedSendUser.user_type })
    })
        .then(r => r.json().then(d => ({ ok: r.ok, data: d })))
        .then(({ ok, data }) => {
            if (!ok) { alert(data.error || 'Ошибка'); return; }
            const link = data.url;
            if (navigator.clipboard) {
                navigator.clipboard.writeText(link).then(() => alert('Ссылка скопирована:' + link));
            } else {
                prompt('Ссылка приглашение:', link);
            }
        })
        .catch(() => alert('Ошибка связи'));
}

var pendingOpenUserMessages = null;

// Открыть переписку с пользователем — можно вызвать до загрузки профиля,
// тогда отложит до момента когда isAdminUser станет известен.
function handleOpenUserMessages(userId, userType) {
    if (isAdminUser) {
        openAdminMessages();
    } else {
        pendingOpenUserMessages = { id: userId, type: userType || 0 };
    }
}
// === МОДАЛЬНЫЕ ОКНА ===

function showAdminReplyModal(messageId, messageText, replyText) {
    currentAdminReplyId = messageId;
    document.getElementById('admin-reply-text').textContent = messageText || '';
    document.getElementById('admin-reply-reply').textContent = replyText || '';
    document.getElementById('admin-reply-modal').style.display = 'block';
}

function showAdminMessageModal(messageId, messageText) {
    // сразу фиксируем прочтение
    fetch(withBase('/api/user/admin-reply'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message_id: messageId, reply: '' })
    }).catch(() => {});
    currentAdminMessageId = messageId;
    document.getElementById('admin-message-text').textContent = messageText || '';
    document.getElementById('admin-message-reply').value = '';
    document.getElementById('admin-message-modal').style.display = 'block';
}

function hideAdminReplyModal() {
    document.getElementById('admin-reply-modal').style.display = 'none';
}

document.addEventListener('DOMContentLoaded', () => {
    document.getElementById('admin-reply-ack').addEventListener('click', () => {
        if (!currentAdminReplyId) { hideAdminReplyModal(); return; }
        fetch(withBase('/api/admin/reply/ack'), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ message_id: currentAdminReplyId })
        })
            .then(res => res.json().then(data => ({ ok: res.ok, data })))
            .then(({ ok, data }) => {
                if (!ok) { alert(data.error || "Не удалось подтвердить"); return; }
                currentAdminReplyId = 0;
                hideAdminReplyModal();
            })
            .catch(() => alert("Ошибка связи"));
    });

    document.getElementById('admin-message-send').addEventListener('click', (e) => {
        const reply = document.getElementById('admin-message-reply').value.trim();
        if (!currentAdminMessageId) return;
        // пустой ответ — закрываем модалку и помечаем прочитанным на сервере
        if (!reply) {
            fetch(withBase('/api/user/admin-reply'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ message_id: currentAdminMessageId, reply: '' })
            }).catch(() => {});
            currentAdminMessageId = 0;
            document.getElementById('admin-message-modal').style.display = 'none';
            return;
        }
        const btn = e.currentTarget;
        btn.disabled = true;
        fetch(withBase('/api/user/admin-reply'), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ message_id: currentAdminMessageId, reply })
        })
            .then(res => res.json().then(data => ({ ok: res.ok, data })))
            .then(({ ok, data }) => {
                if (!ok) { alert(data.error || "Не удалось отправить ответ"); btn.disabled = false; return; }
                currentAdminMessageId = 0;
                document.getElementById('admin-message-modal').style.display = 'none';
            })
            .catch(() => { alert("Ошибка связи"); btn.disabled = false; });
    });

    document.getElementById('ios-install-close').addEventListener('click', () => {
        document.getElementById('ios-install-modal').style.display = 'none';
    });
});

// === СООБЩЕНИЕ АДМИНИСТРАТОРУ ===

function messageAdmin() {
    toggleMenu();
    const text = prompt("Введите сообщение администратору:");
    if (text === null) return;
    const message = text.trim();
    if (!message) { alert("Пустое сообщение"); return; }

    fetch(withBase('/api/admin/message'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ message })
    })
        .then(res => res.json().then(data => ({ ok: res.ok, data })))
        .then(({ ok, data }) => {
            if (!ok) alert(data.error || "Не удалось отправить сообщение");
        })
        .catch(() => alert("Ошибка связи при отправке"));
}

async function logout() {
    await unsubscribeWebPush();
    window.location.href = withBase('/auth/logout');
}

// === АВТОМАТ ===

function openMachineList() {
    const value = prompt("Введите номер автомата (VMID).\n0 — авто по геолокации.");
    if (value === null) return;
    const vmid = parseInt(value, 10);
    if (Number.isNaN(vmid) || vmid < 0) return alert("Некорректный номер автомата");
    if (vmid === 0) return setAutoFavoriteByLocation();
    saveFavorite(vmid);
}

function askMachineManualAndSave() {
    const manual = prompt("С каким автоматом работаем? Введите VMID:");
    if (manual === null) return;
    const vmid = parseInt(manual, 10);
    if (Number.isNaN(vmid) || vmid <= 0) return alert("Некорректный VMID");
    saveFavorite(vmid);
}

function setAutoFavoriteByLocation() {
    if (!navigator.geolocation) { askMachineManualAndSave(); return; }
    navigator.geolocation.getCurrentPosition(
        (pos) => saveFavorite(0, pos.coords.latitude, pos.coords.longitude),
        () => askMachineManualAndSave(),
        { enableHighAccuracy: true, timeout: 8000, maximumAge: 60000 }
    );
}

function saveFavorite(vmid, lat = null, lon = null) {
    const payload = { vmid: parseInt(vmid, 10) };
    if (lat !== null && lon !== null) { payload.lat = lat; payload.lon = lon; }
    fetch(withBase('/api/favorite'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
    })
        .then(res => res.json())
        .then(data => {
            if (data.status === 'ok') { location.reload(); return; }
            if (data.status === 'need_machine_select') { askMachineManualAndSave(); return; }
            alert("Не удалось сохранить автомат");
        })
        .catch(() => alert("Ошибка сохранения автомата"));
}

// === ПОПУЛЯРНЫЕ НАПИТКИ ===

function loadPopular() {
    fetch(withBase('/api/drinks/popular'))
        .then(res => res.json())
        .then(drinks => {
            const container = document.getElementById('popular-drinks');
            container.innerHTML = drinks.map(d => {
                const creamTxt = d.cream !== 4 ? `🥛${d.cream}` : '';
                const sugarTxt = d.sugar !== 4 ? `🍬${d.sugar}` : '';
                const extras = (creamTxt || sugarTxt) ? `<br><small>${creamTxt} ${sugarTxt}</small>` : '';
                return `<button onclick="setPreset('${d.drink}', ${d.cream}, ${d.sugar})"
                    style="padding:10px 15px; border-radius:10px; border:1px solid #0088cc; background:#fff; font-size:14px; min-width:60px;">
                    <b style="font-size:28px;">${d.drink}</b>${extras}
                </button>`;
            }).join('');
        });
}

function setPreset(drink, cream, sugar) {
    document.getElementById('drink-id').value = drink;
    document.getElementById('cream').value = cream;
    document.getElementById('sugar').value = sugar;
    document.getElementById('val-c').innerText = cream;
    document.getElementById('val-s').innerText = sugar;
}

// === СТАТУС АВТОМАТА В ВЕРХНЕМ БАРЕ ===

// Маппинг enum vender_api.State → отображаемый текст и цвет.
// См. tele.proto: 0=Invalid 1=Boot 2=Nominal 3=Client 4=Broken 5=Service
//                6=Lock 7=Process 8=TempProblem 9=Shutdown 10=RemoteControl
//                11=WaitingForExternalPayment
const MACHINE_STATE_MAP = {
    0:  { text: 'не в сети',      color: '#bbb' },
    1:  { text: 'загрузка',       color: '#bbb' },
    2:  { text: 'готов выполнить заказ',         color: '#2ecc71' },
    3:  { text: 'работает с клиентом',         color: '#f1c40f' },
    4:  { text: 'сломан',         color: '#e74c3c' },
    5:  { text: 'обслуживание',   color: '#e67e22' },
    6:  { text: 'заблокирован',  color: '#e67e22' },
    7:  { text: 'готовит',         color: '#f1c40f' },
    8:  { text: 'ошибка t°',      color: '#e74c3c' },
    9:  { text: 'выключен',     color: '#bbb' },
    10: { text: 'удалённое управление',      color: '#bbb' },
    11: { text: 'работает с клиентом. ждёт оплату.',    color: '#f1c40f' },
};

let machineStatusVmid = null;
let machineStatusWS = null;
let machineStatusReconnectTimer = null;

function setMachineStatusLabel(text, color) {
    const el = document.getElementById('machine-status-label');
    if (!el) return;
    el.innerText = text;
    el.style.color = color || '';
}

function applyMachineStatus(ev) {
    if (!ev.connect) {
        setMachineStatusLabel('offline', '#bbb');
        setOrderButtonAvailable(false);
        return;
    }
    const meta = MACHINE_STATE_MAP[ev.state] || { text: '', color: '' };
    setMachineStatusLabel(meta.text, meta.color);
    // Заказ доступен только в состоянии Nominal (2)
    setOrderButtonAvailable(ev.state === 2);
}

function setOrderButtonAvailable(available) {
    const btn = document.querySelector('#order-card .btn-blue');
    if (!btn) return;
    btn.disabled = !available;
    btn.style.opacity = available ? '1' : '0.2';
    btn.title = available ? '' : 'Автомат сейчас не может выполнить заказ';
}

function openMachineStatusWS() {
    if (!machineStatusVmid) return;
    if (machineStatusWS && machineStatusWS.readyState <= 1) return;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const url = proto + '//' + location.host + withBase('/api/machine/status/ws')
        + '?vmid=' + encodeURIComponent(machineStatusVmid);
    const ws = new WebSocket(url);
    machineStatusWS = ws;

    ws.onmessage = (e) => {
        try {
            applyMachineStatus(JSON.parse(e.data));
        } catch (_) { /* ignore */ }
    };

    ws.onclose = () => {
        if (ws !== machineStatusWS) return;
        machineStatusWS = null;
        // Переподключаемся только если страница активна и vmid известен.
        if (document.visibilityState === 'visible' && machineStatusVmid) {
            clearTimeout(machineStatusReconnectTimer);
            machineStatusReconnectTimer = setTimeout(openMachineStatusWS, 2000);
        }
    };
    ws.onerror = () => { /* обрабатываем в onclose */ };
}

function closeMachineStatusWS() {
    clearTimeout(machineStatusReconnectTimer);
    machineStatusReconnectTimer = null;
    if (machineStatusWS) {
        const ws = machineStatusWS;
        machineStatusWS = null;
        try { ws.close(); } catch (_) { /* ignore */ }
    }
}

function startMachineStatusWS(vmid) {
    machineStatusVmid = vmid;
    if (document.visibilityState === 'visible') openMachineStatusWS();
}

document.addEventListener('visibilitychange', () => {
    if (document.visibilityState === 'visible') {
        if (machineStatusVmid) openMachineStatusWS();
    } else {
        closeMachineStatusWS();
    }
});

// === ЗАКАЗ ===

function makeOrder() {
    const drink = document.getElementById('drink-id').value;
    const cream = parseInt(document.getElementById('cream').value);
    const sugar = parseInt(document.getElementById('sugar').value);

    if (!drink) return alert("Введите код напитка");
    if (!currentDefaultVmid) return alert("Выберите автомат в меню ☰");

    fetch(withBase('/api/order/check'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ vmid: parseInt(currentDefaultVmid), drink: String(drink), cream, sugar })
    })
        .then(res => res.json().catch(() => { throw new Error('HTTP ' + res.status) }))
        .then(data => {
            if (data.error) { alert("Ошибка: " + data.error); return; }
            pendingOrder = { vmid: parseInt(currentDefaultVmid), drink_code: String(drink), cream, sugar };
            document.getElementById('conf-vmid').innerText = currentDefaultVmid;
            document.getElementById('conf-name').innerText = data.drink_name || '—';
            document.getElementById('conf-code').innerText = drink;
            document.getElementById('status-msg').innerText = '';
            _showConfirmScreen();
            history.pushState({ orderScreen: true }, '');
        })
        .catch(err => alert("Ошибка: " + (err.message || "связи с сервером")));
}

// Подключается к WebSocket заказа с автоматическим переподключением при обрыве.
// onMessage вызывается при каждом сообщении от сервера.
// onFail вызывается когда все попытки исчерпаны.
// Возвращает объект { close() } для принудительного закрытия снаружи.
function connectOrderWSWithRetry(onMessage, onFail, attempt = 0) {
    const MAX_ATTEMPTS = 10;
    const RETRY_DELAY_MS = 2000;
    const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(proto + '//' + location.host + withBase('/api/order/ws'));
    const handle = { ws, closed: false };

    ws.onopen = () => {
        // при переподключении НЕ отправляем заказ повторно —
        // только восстанавливаем канал для получения статуса
        if (attempt === 0) {
            showStatus('⏳ Подключился, отправляю заказ...');
        } else {
            showStatus('⏳ Переподключился, жду ответ автомата...');
        }
        // сразу сообщаем серверу видимость окна
        try { ws.send(JSON.stringify({ type: 'visibility', hidden: document.hidden })); } catch (_) {}
    };

    ws.onmessage = onMessage;
    ws.onerror = () => {}; // обрабатываем в onclose

    // при смене видимости окна — уведомляем сервер чтобы он не слал push когда окно открыто
    handle._visibilityHandler = () => {
        if (ws.readyState === WebSocket.OPEN) {
            try { ws.send(JSON.stringify({ type: 'visibility', hidden: document.hidden })); } catch (_) {}
        }
    };
    document.addEventListener('visibilitychange', handle._visibilityHandler);

    ws.onclose = () => {
        if (handle._visibilityHandler) {
            document.removeEventListener('visibilitychange', handle._visibilityHandler);
            handle._visibilityHandler = null;
        }
        if (handle.closed) return; // закрыто намеренно снаружи
        if (attempt < MAX_ATTEMPTS) {
            showStatus(`⏳ Связь прервана, переподключаюсь... (${attempt + 1}/${MAX_ATTEMPTS})`);
            setTimeout(() => {
                if (!handle.closed) {
                    const next = connectOrderWSWithRetry(onMessage, onFail, attempt + 1);
                    handle.ws = next.ws;
                }
            }, RETRY_DELAY_MS);
        } else {
            onFail();
        }
    };

    return handle;
}

function startBrewing() {
    let orderFinished = false;
    let orderSent = false;
    if (!pendingOrder) return;
    requestNotificationPermission();

    const btn = document.getElementById('btn-brew');
    btn.disabled = true;
    btn.innerText = 'Отправляю...';
    document.getElementById('btn-cancel').style.display = 'none';
    showStatus('⏳ Подключаюсь к автомату...');

    // Общий таймаут 60 секунд — если автомат вообще не ответил
    let wsHandle = null;
    const timeout = setTimeout(() => {
        orderFinished = true;
        if (wsHandle) wsHandle.closed = true, wsHandle.ws.close();
        showStatus('❌ Таймаут — автомат не ответил');
        showOkButton();
    }, 60000);

    wsHandle = connectOrderWSWithRetry(
        // onMessage — обработка событий от автомата
        (e) => {
            console.log('WS message:', e.data);
            clearTimeout(timeout);
            const event = JSON.parse(e.data);
            switch (event.status) {
                case 'executionStart':
                    // showStatus('☕ Готовлю... стоимость заказа:' + (event.amount ? event.amount.toFixed(2) + ' ₽' : ''));
                    showStatus('☕ Готовлю... код: ' + (pendingOrder ? pendingOrder.drink_code : '') + '  стоимость: ' + (event.amount ? event.amount.toFixed(2) + ' ₽' : ''));
                    btn.style.display = 'none';
                    break;
                case 'complete':
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    playDoneSound();
                    // notifyOrderReady(event);
                    refreshBalance();
                    // Если пользователь не уходил с экрана приготовления — сбрасываем форму к дефолту.
                    // Если ушёл на главный, форму не трогаем: можно начать вводить следующий заказ.
                    if (document.getElementById('confirm-card').style.display !== 'none') {
                        document.getElementById('drink-id').value = '';
                        document.getElementById('cream').value = 4;
                        document.getElementById('sugar').value = 4;
                        document.getElementById('val-c').innerText = 4;
                        document.getElementById('val-s').innerText = 4;
                    }
                    // сбрасываем состояние кнопок и заказа
                    pendingOrder = null;
                    document.getElementById('btn-brew').style.display = '';
                    document.getElementById('btn-brew').disabled = false;
                    document.getElementById('btn-brew').innerText = 'ПРИГОТОВИТЬ';
                    document.getElementById('btn-ok').style.display = 'none';
                    document.getElementById('btn-cancel').style.display = 'inline';
                    document.getElementById('status-msg').innerText = '';
                    // возвращаемся на главный экран с финальным статусом в баннере
                    if (history.state && history.state.orderScreen) history.back();
                    _hideConfirmScreen();
                    showStatus(event.cashback
                        ? '✅ Готово! Приятного аппетита. Кэшбек: ' + event.cashback.toFixed(2) + ' ₽'
                        : '✅ Готово! Приятного аппетита.', true);
                    // убираем баннер через 5 секунд
                    setTimeout(() => {
                        const banner = document.getElementById('order-status-banner');
                        if (banner) banner.style.display = 'none';
                    }, 5000);
                    break;
                case 'executionInaccessible':
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    showStatus('❌ Код недоступен или мало ингредиентов.');
                    showOkButton(); break;
                case 'overdraft':
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    showStatus('💸 Недостаточно средств.');
                    showOkButton(); break;
                case 'robotIsBusy':
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    showStatus('🔄 Автомат занят, попробуйте позже.');
                    showOkButton(); break;
                default:
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    alert(event.message || event.status);
                    cancelOrder(); break;
            }
        },

        // onFail — все попытки переподключения исчерпаны
        () => {
            clearTimeout(timeout);
            if (!orderFinished) {
                showStatus('⚠️ Нет связи с сервером. Проверьте интернет.');
                showOkButton();
            }
        }
    );

    // Отправляем заказ один раз — сразу после первого подключения
    // Используем polling на ws.readyState чтобы дождаться onopen
    const sendOrder = () => {
        if (orderSent || orderFinished) return;
        if (wsHandle.ws.readyState === WebSocket.OPEN) {
            orderSent = true;
            fetch(withBase('/api/order/start'), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(pendingOrder)
            })
                .then(res => res.json())
                .then(data => {
                    if (data.error) {
                        clearTimeout(timeout);
                        orderFinished = true;
                        wsHandle.closed = true;
                        wsHandle.ws.close();
                        showStatus('❌ ' + data.error);
                        btn.disabled = false;
                        btn.innerText = 'ПРИГОТОВИТЬ';
                        return;
                    }
                    showStatus('⏳ Команда отправлена, жду автомат...');
                })
                .catch(() => {
                    clearTimeout(timeout);
                    orderFinished = true;
                    wsHandle.closed = true;
                    wsHandle.ws.close();
                    showStatus('❌ Ошибка связи при отправке заказа');
                    btn.disabled = false;
                    btn.innerText = 'ПРИГОТОВИТЬ';
                });
        } else {
            // WS ещё не открыт — ждём
            setTimeout(sendOrder, 50);
        }
    };
    sendOrder();
}

function showOkButton() {
    document.getElementById('btn-brew').style.display = 'none';
    document.getElementById('btn-ok').style.display = '';
}

function cancelOrder() {
    if (history.state && history.state.orderScreen) {
        history.back();
    }
    document.getElementById('confirm-card').style.display = 'none';
    document.getElementById('order-card').style.display = 'block';
    document.getElementById('btn-brew').style.display = '';
    document.getElementById('btn-brew').disabled = false;
    document.getElementById('btn-brew').innerText = 'ПРИГОТОВИТЬ';
    document.getElementById('btn-ok').style.display = 'none';
    document.getElementById('btn-cancel').style.display = 'inline';
    document.getElementById('status-msg').innerText = '';
    pendingOrder = null;
}

function _hideConfirmScreen() {
    document.getElementById('confirm-card').style.display = 'none';
    document.getElementById('order-card').style.display = 'block';
    // показываем текущий статус на главном экране если заказ ещё идёт
    const currentStatus = document.getElementById('status-msg').innerText;
    if (currentStatus) showStatus(currentStatus);
}

function _showConfirmScreen() {
    const banner = document.getElementById('order-status-banner');
    if (banner) banner.style.display = 'none';
    document.getElementById('order-card').style.display = 'none';
    document.getElementById('confirm-card').style.display = 'block';
}

// Кнопка "назад" браузера/телефона во время экрана заказа —
// просто скрываем экран приготовления, заказ продолжается в фоне.
window.addEventListener('popstate', () => {
    const confirmVisible = document.getElementById('confirm-card').style.display !== 'none';
    if (confirmVisible) {
        _hideConfirmScreen();
    }
});

function showStatus(msg, hideBackButton = false) {
    document.getElementById('status-msg').innerText = msg;

    // Если пользователь вернулся на главный экран — показываем статус там
    const confirmHidden = document.getElementById('confirm-card').style.display === 'none';
    const orderVisible = document.getElementById('order-card').style.display !== 'none';
    if (confirmHidden && orderVisible && msg) {
        let banner = document.getElementById('order-status-banner');
        if (!banner) {
            banner = document.createElement('div');
            banner.id = 'order-status-banner';
            banner.style.cssText = 'padding:12px; border-radius:10px; background:#e8f4fd; color:#0088cc; text-align:center; font-size:15px; margin-bottom:12px;';
            banner.innerHTML =
                '<div id="order-status-text" style="font-weight:bold; margin-bottom:6px;"></div>' +
                '<button id="order-back-btn" onclick="_showConfirmScreen(); history.pushState({orderScreen:true},\'\')" ' +
                'style="font-size:13px; padding:5px 14px; border:1px solid #0088cc; border-radius:8px; background:#fff; color:#0088cc; cursor:pointer;">← К заказу</button>';
            // вставляем перед карточкой заказа — прямо над ней, внутри #user-info
            const userInfo = document.getElementById('user-info');
            const orderCard = document.getElementById('order-card');
            userInfo.insertBefore(banner, orderCard);
        }
        document.getElementById('order-status-text').innerText = msg;
        const backBtn = document.getElementById('order-back-btn');
        if (backBtn) backBtn.style.display = hideBackButton ? 'none' : 'inline-block';
        banner.style.display = 'block';
    } else {
        const banner = document.getElementById('order-status-banner');
        if (banner) banner.style.display = 'none';
    }
}

function playDoneSound() {
    try {
        const AudioCtx = window.AudioContext || window.webkitAudioContext;
        if (!AudioCtx) return;
        const ctx = new AudioCtx();
        const now = ctx.currentTime;
        const freqs = [880, 1175, 1568];
        freqs.forEach((f, i) => {
            const osc = ctx.createOscillator();
            const gain = ctx.createGain();
            osc.type = 'sine';
            osc.frequency.value = f;
            gain.gain.setValueAtTime(0.0001, now + i * 0.12);
            gain.gain.exponentialRampToValueAtTime(0.12, now + i * 0.12 + 0.02);
            gain.gain.exponentialRampToValueAtTime(0.0001, now + i * 0.12 + 0.18);
            osc.connect(gain);
            gain.connect(ctx.destination);
            osc.start(now + i * 0.12);
            osc.stop(now + i * 0.12 + 0.2);
        });
    } catch (_) { }
}

// === УВЕДОМЛЕНИЯ ===

async function requestNotificationPermission() {
    if (!('Notification' in window)) return;
    if (Notification.permission === 'default') {
        try { await Notification.requestPermission(); } catch (_) { }
    }
    if (Notification.permission === 'granted') {
        await ensureWebPushSubscription();
    }
}

async function notifyOrderReady(event) {
    if (!('Notification' in window)) return;
    if (Notification.permission !== 'granted') return;
    if (!document.hidden && document.hasFocus()) return;
    const drinkCode = pendingOrder ? ` (${pendingOrder.drink_code})` : '';
    const body = event && event.cashback
        ? `Напиток готов${drinkCode}. Кэшбек: ${event.cashback.toFixed(2)} ₽`
        : `Ваш напиток готов${drinkCode}. Приятного аппетита.`;
    const targetURL = location.origin + withBase('/');
    const iconURL = location.origin + withBase('/icon-192.png');

    try {
        if (navigator.serviceWorker && navigator.serviceWorker.ready) {
            const reg = await navigator.serviceWorker.ready;
            await reg.showNotification('Vender Web', {
                body, tag: 'order-ready', renotify: true,
                icon: iconURL, badge: iconURL, data: { url: targetURL }
            });
            return;
        }
    } catch (_) { }
    try { new Notification('Vender Web', { body, icon: iconURL }); } catch (_) { }
}

function urlBase64ToUint8Array(base64String) {
    const padding = '='.repeat((4 - (base64String.length % 4)) % 4);
    const base64 = (base64String + padding).replace(/-/g, '+').replace(/_/g, '/');
    const rawData = window.atob(base64);
    const outputArray = new Uint8Array(rawData.length);
    for (let i = 0; i < rawData.length; ++i) outputArray[i] = rawData.charCodeAt(i);
    return outputArray;
}

async function ensureWebPushSubscription() {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
    if (Notification.permission !== 'granted') return;

    const reg = swRegistrationPromise ? await swRegistrationPromise : await navigator.serviceWorker.ready;
    if (!reg) return;

    const keyResp = await fetch(withBase('/api/push/public-key'));
    if (!keyResp.ok) return;
    const keyData = await keyResp.json();
    if (!keyData.publicKey) return;

    try {
        // ждём активного SW перед подпиской
        await navigator.serviceWorker.ready;
        let subscription = await reg.pushManager.getSubscription();
        if (subscription) await subscription.unsubscribe();
        subscription = await reg.pushManager.subscribe({
            userVisibleOnly: true,
            applicationServerKey: urlBase64ToUint8Array(keyData.publicKey)
        });
        const resp = await fetch(withBase('/api/push/subscribe'), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(subscription)
        });
        if (!resp.ok) {
            console.error('push subscribe error', resp.status, await resp.text());
        } else {
            console.log('push subscribe ok', subscription.endpoint);
        }
    } catch (e) {
        console.error('push subscribe exception', e);
    }
}

async function unsubscribeWebPush() {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
    const reg = swRegistrationPromise ? await swRegistrationPromise : await navigator.serviceWorker.ready;
    if (!reg) return;
    const subscription = await reg.pushManager.getSubscription();
    if (!subscription) return;
    try {
        await fetch(withBase('/api/push/unsubscribe'), {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ endpoint: subscription.endpoint })
        });
    } catch (_) { }
    try { await subscription.unsubscribe(); } catch (_) { }
}

async function togglePushNotifications() {
    const item = document.getElementById('push-toggle-item');
    const reg = swRegistrationPromise ? await swRegistrationPromise : await navigator.serviceWorker.ready;
    const subscription = reg ? await reg.pushManager.getSubscription() : null;

    if (subscription) {
        await unsubscribeWebPush();
        item.innerText = '🔔 Включить уведомления';
        toggleMenu();
        return;
    }

    const permission = Notification.permission === 'granted'
        ? 'granted'
        : await Notification.requestPermission();

    if (permission !== 'granted') {
        alert('Разрешите уведомления в настройках браузера');
        toggleMenu();
        return;
    }

    await ensureWebPushSubscription();
    item.innerText = '🔕 Выключить уведомления';
    toggleMenu();
}

async function updatePushMenuItem() {
    if (!('serviceWorker' in navigator) || !('PushManager' in window)) return;
    if (typeof window.TelegramWebviewProxy !== 'undefined') return;
    const item = document.getElementById('push-toggle-item');
    if (!item) return;
    item.style.display = 'block';
    const reg = swRegistrationPromise ? await swRegistrationPromise : await navigator.serviceWorker.ready;
    const subscription = reg ? await reg.pushManager.getSubscription() : null;
    item.innerText = subscription ? '🔕 Выключить уведомления' : '🔔 Включить уведомления';
}

// === ЗАГРУЗКА ПРОФИЛЯ ===

function refreshBalance() {
    fetch(withBase('/api/balance'))
        .then(res => res.ok ? res.json() : Promise.reject())
        .then(data => {
            const balanceElem = document.getElementById('balance');
            const creditStatus = document.getElementById('credit-status');
            const welcomeElem = document.getElementById('welcome-user');
            if (!balanceElem) return;
            balanceElem.innerText = data.balance.toFixed(2);
            if (data.balance < 0) {
                balanceElem.style.color = '#e74c3c';
                creditStatus.style.display = 'block';
                welcomeElem.innerText = `Приветствую, должничок ${data.user_name || ''}!`;
            } else {
                balanceElem.style.color = '#2ecc71';
                creditStatus.style.display = 'none';
                welcomeElem.innerText = `Приветствую, ${data.user_name || 'мой господин'}!`;
            }
        })
        .catch(() => {});
}

fetch(withBase('/api/balance'))
    .then(res => res.ok ? res.json() : Promise.reject())
    .then(data => {
        document.getElementById('auth-section').style.display = 'none';
        document.getElementById('user-info').style.display = 'block';
        document.getElementById('nav-bar').style.display = 'flex';

        const welcomeElem = document.getElementById('welcome-user');
        const balanceElem = document.getElementById('balance');
        const creditStatus = document.getElementById('credit-status');

        if (data.balance < 0) {
            welcomeElem.innerText = `Приветствую, должничок ${data.user_name || ''}!`;
            balanceElem.style.color = "#e74c3c";
            creditStatus.style.display = "block";
        } else {
            welcomeElem.innerText = `Приветствую, ${data.user_name || 'мой господин'}!`;
            balanceElem.style.color = "#2ecc71";
            creditStatus.style.display = "none";
        }

        balanceElem.innerText = data.balance.toFixed(2);

        const discElem = document.getElementById('discount-info');
        const credElem = document.getElementById('credit-info');
        const sepElem = document.getElementById('perks-separator');

        if (data.discount > 0) discElem.innerHTML = `🏷️скидка <b>${data.discount}%</b>`;
        if (data.credit > 0) credElem.innerHTML = `💳кредит <b>${data.credit.toFixed(2)}₽</b>`;
        if (data.discount > 0 && data.credit > 0) sepElem.style.display = 'inline';

        currentDefaultVmid = data.vm_id;
        isAdminUser = Boolean(data.is_admin);

        if (isAdminUser && window.__openAdminMessages) {
            setTimeout(() => openAdminMessages(), 300);
        }
        if (isAdminUser && (window.__openUserMessages || pendingOpenUserMessages)) {
            pendingOpenUserMessages = null;
            setTimeout(() => openAdminMessages(), 300);
        }

        const adminItem = document.getElementById('admin-messages-item');
        if (adminItem) adminItem.style.display = isAdminUser ? 'block' : 'none';
        const adminPageItem = document.getElementById('admin-page-item');
        if (adminPageItem) adminPageItem.style.display = isAdminUser ? 'block' : 'none';
        const adminSendItem = document.getElementById('admin-send-item');
        if (adminSendItem) adminSendItem.style.display = isAdminUser ? 'block' : 'none';
        const iosItem = document.getElementById('ios-install-item');
        if (iosItem) iosItem.style.display = (!isStandaloneApp() && isIOSDevice()) ? 'block' : 'none';
        document.getElementById('current-machine-label').innerText = data.vm_id ? `Автомат №${data.vm_id}` : "Выберите автомат";
        if (data.vm_id) startMachineStatusWS(data.vm_id);

        if (data.admin_reply && data.admin_reply.id) {
            showAdminReplyModal(data.admin_reply.id, data.admin_reply.message || '', data.admin_reply.reply || '');
        }
        if (data.admin_message && data.admin_message.id) {
            showAdminMessageModal(data.admin_message.id, data.admin_message.message || '');
        }
        if (Notification.permission === 'granted') {
            ensureWebPushSubscription().catch(() => { });
        } else if (_params.get('new') === '1') {
            // новый пользователь — запрашиваем разрешение автоматически
            requestNotificationPermission().catch(() => { });
        }
        loadPopular();
        updatePushMenuItem();
    })
    .catch(() => console.log("Нужен логин")
);
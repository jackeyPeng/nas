// Z1 NAS i18n Framework
// Language packs are JSON files in i18n/ directory
// zh-CN.json is the source of truth; other languages should be kept in sync
// Usage: t('key.path') or $t('key.path') in Alpine.js templates
//
// NOTE: language packs are loaded SYNCHRONOUSLY at startup so that window.$t
// is defined and populated before Alpine (defer) evaluates the templates.
// Otherwise $t is undefined at first render and all i18n text is blank.
//
// Reactivity: $t is ALSO registered as an Alpine magic that reads
// Alpine.store('i18n').lang. This establishes a reactive dependency so that
// switching language re-renders every $t(...) expression automatically.

(function() {
    'use strict';

    const I18N_DIR = 'i18n';
    let currentLang = localStorage.getItem('nas_lang') || '';
    let translations = {};
    let fallbackTranslations = {};

    // Detect browser language
    function detectLang() {
        if (currentLang) return currentLang;
        const navLang = (navigator.language || navigator.userLanguage || '').toLowerCase();
        if (navLang.startsWith('zh')) return 'zh-CN';
        if (navLang.startsWith('en')) return 'en-US';
        if (navLang.startsWith('ja')) return 'ja-JP';
        return 'zh-CN'; // default
    }

    // Synchronously load a language pack (blocking, so $t is ready before Alpine starts)
    function loadLangSync(lang) {
        try {
            const xhr = new XMLHttpRequest();
            xhr.open('GET', I18N_DIR + '/' + lang + '.json', false); // false = sync
            xhr.overrideMimeType('application/json');
            xhr.send(null);
            if (xhr.status >= 200 && xhr.status < 300) {
                return JSON.parse(xhr.responseText);
            }
        } catch (e) {
            console.warn('[i18n] Failed to load ' + lang + ':', e && e.message);
        }
        return null;
    }

    // Get nested key value
    function getNested(obj, path) {
        const keys = path.split('.');
        let current = obj;
        for (const k of keys) {
            if (current == null || typeof current !== 'object') return null;
            current = current[k];
        }
        return current;
    }

    // Translate against a given pack with zh-CN fallback
    function translateWith(trans, key, params) {
        let val = getNested(trans, key);
        if (val == null && fallbackTranslations) {
            val = getNested(fallbackTranslations, key);
        }
        if (val == null) {
            console.debug('[i18n] Missing key: ' + key);
            return key.split('.').pop(); // return last segment as hint
        }
        if (params && typeof val === 'string') {
            for (let i = 0; i < params.length; i++) {
                val = val.replace('{' + i + '}', params[i]);
            }
        }
        return val;
    }

    // Plain (non-reactive) translator for imperative JS use
    function t(key, params) {
        return translateWith(translations, key, params);
    }

    // Switch language. Updates the closure state AND the reactive Alpine store.
    function switchLang(lang) {
        const data = loadLangSync(lang);
        if (!data) return false;
        translations = data;
        currentLang = lang;
        localStorage.setItem('nas_lang', lang);
        // Trigger reactive re-render via the Alpine store
        try {
            const store = window.Alpine && window.Alpine.store && window.Alpine.store('i18n');
            if (store) store.lang = lang;
        } catch (e) { /* Alpine not ready yet — event still fires below */ }
        window.dispatchEvent(new CustomEvent('i18n:changed', { detail: { lang: lang } }));
        return true;
    }

    // Initialize synchronously (not async) so $t is defined before Alpine starts
    function init() {
        currentLang = detectLang();
        // Always load zh-CN as fallback
        fallbackTranslations = loadLangSync('zh-CN') || {};
        // Load current language
        const data = loadLangSync(currentLang);
        if (data) {
            translations = data;
        } else if (currentLang !== 'zh-CN') {
            // If current lang fails, fall back to zh-CN
            translations = fallbackTranslations;
            currentLang = 'zh-CN';
        } else {
            translations = fallbackTranslations;
        }
        // Expose globally (t for JS, $t for non-Alpine templates)
        window.t = t;
        window.$t = t;
        window.switchLang = switchLang;
        window.currentLang = currentLang;
        window.availableLangs = ['zh-CN', 'en-US'];
        window.dispatchEvent(new CustomEvent('i18n:ready', { detail: { lang: currentLang } }));
    }

    // Register a reactive store + $t magic BEFORE Alpine.start() walks the DOM.
    document.addEventListener('alpine:init', function() {
        if (!window.Alpine || typeof window.Alpine.store !== 'function') return;
        window.Alpine.store('i18n', { lang: currentLang });
        window.Alpine.magic('t', function() {
            return function(key, params) {
                // Reading store.lang establishes the reactive dependency so that
                // a language switch re-renders every $t(...) expression.
                window.Alpine.store('i18n').lang;
                return t(key, params);
            };
        });
    });

    try {
        init();
    } catch (err) {
        console.error('[i18n] Init failed:', err);
        // Bare minimum fallback
        window.t = function(key) { return key.split('.').pop(); };
        window.$t = window.t;
        window.switchLang = async function() {};
        window.currentLang = 'zh-CN';
        window.availableLangs = ['zh-CN'];
    }
})();

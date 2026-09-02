// Z1 NAS i18n Framework
// Language packs are JSON files in i18n/ directory
// zh-CN.json is the source of truth; other languages should be kept in sync
// Usage: t('key.path') or $t('key.path') in Alpine.js templates

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

    // Load a language pack
    async function loadLang(lang) {
        try {
            const resp = await fetch(`${I18N_DIR}/${lang}.json`);
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            return await resp.json();
        } catch (e) {
            console.warn(`[i18n] Failed to load ${lang}:`, e.message);
            return null;
        }
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

    // Translation function
    function t(key, params) {
        // Try current language
        let val = getNested(translations, key);
        // Fallback to zh-CN
        if (val == null && fallbackTranslations) {
            val = getNested(fallbackTranslations, key);
        }
        // Fallback to key name
        if (val == null) {
            console.debug(`[i18n] Missing key: ${key}`);
            return key.split('.').pop(); // return last segment as hint
        }
        // Replace params {0}, {1}, ...
        if (params && typeof val === 'string') {
            for (let i = 0; i < params.length; i++) {
                val = val.replace(`{${i}}`, params[i]);
            }
        }
        return val;
    }

    // Switch language
    async function switchLang(lang) {
        const data = await loadLang(lang);
        if (data) {
            translations = data;
            currentLang = lang;
            localStorage.setItem('nas_lang', lang);
            // Dispatch event so Alpine.js can re-render
            window.dispatchEvent(new CustomEvent('i18n:changed', { detail: { lang } }));
            return true;
        }
        return false;
    }

    // Initialize
    async function init() {
        currentLang = detectLang();
        // Always load zh-CN as fallback
        fallbackTranslations = await loadLang('zh-CN') || {};
        // Load current language
        const data = await loadLang(currentLang);
        if (data) {
            translations = data;
        } else if (currentLang !== 'zh-CN') {
            // If current lang fails, fall back to zh-CN
            translations = fallbackTranslations;
            currentLang = 'zh-CN';
        } else {
            translations = fallbackTranslations;
        }
        // Expose globally
        window.t = t;
        window.switchLang = switchLang;
        window.currentLang = currentLang;
        window.availableLangs = ['zh-CN', 'en-US'];
        window.dispatchEvent(new CustomEvent('i18n:ready', { detail: { lang: currentLang } }));
    }

    // Start loading
    init().catch(err => {
        console.error('[i18n] Init failed:', err);
        // Bare minimum fallback
        window.t = function(key) { return key.split('.').pop(); };
        window.switchLang = async function() {};
        window.currentLang = 'zh-CN';
        window.availableLangs = ['zh-CN'];
    });
})();
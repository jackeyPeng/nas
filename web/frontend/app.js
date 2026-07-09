function nasPanel() {
    return {
        token: localStorage.getItem('nas_token') || '',
        page: 'dashboard',
        loading: false,
        loginError: '',
        loginForm: { username: '', password: '' },
        dashboard: {},
        services: [],
        users: [],
        storage: {},
        smartStatus: '',
        firewallStatus: '',
        firewallForm: { port: '', proto: 'tcp' },
        monitor: {},
        alertConfig: {},
        logsModal: false,
        logsService: '',
        logsContent: '',
        addUserModal: false,
        addUserForm: { username: '', password: '' },
        pwdModal: false,
        pwdUser: '',
        pwdForm: { password: '' },
        toast: { show: false, msg: '', type: 'success' },

        init() {
            if (this.token) {
                this.navigate('dashboard');
            }
        },

        async api(path, options = {}) {
            const opts = {
                ...options,
                headers: {
                    'Authorization': 'Bearer ' + this.token,
                    ...(options.headers || {})
                }
            };
            const res = await fetch('/api' + path, opts);
            if (res.status === 401) {
                this.logout();
                return null;
            }
            const ct = res.headers.get('content-type');
            if (ct && ct.includes('application/json')) {
                return res.json();
            }
            return res.text();
        },

        async login() {
            this.loading = true;
            this.loginError = '';
            try {
                const res = await fetch('/api/login', {
                    method: 'POST',
                    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                    body: `username=${encodeURIComponent(this.loginForm.username)}&password=${encodeURIComponent(this.loginForm.password)}`
                });
                const data = await res.json();
                if (res.ok) {
                    this.token = data.token;
                    localStorage.setItem('nas_token', this.token);
                    this.navigate('dashboard');
                } else {
                    this.loginError = data.error || '登录失败';
                }
            } catch (e) {
                this.loginError = '网络错误: ' + e.message;
            } finally {
                this.loading = false;
            }
        },

        logout() {
            this.token = '';
            localStorage.removeItem('nas_token');
            this.page = 'dashboard';
        },

        navigate(page) {
            this.page = page;
            switch (page) {
                case 'dashboard': this.loadDashboard(); break;
                case 'services': this.loadServices(); break;
                case 'users': this.loadUsers(); break;
                case 'storage': this.loadStorage(); break;
                case 'firewall': this.loadFirewall(); break;
                case 'monitor': this.loadMonitor(); this.loadAlertConfig(); break;
            }
        },

        async loadDashboard() {
            const data = await this.api('/dashboard');
            if (data) this.dashboard = data;
        },

        async loadServices() {
            const data = await this.api('/services');
            if (data) this.services = data.services || [];
        },

        async loadUsers() {
            const data = await this.api('/users');
            if (data) this.users = data.users || [];
        },

        async loadStorage() {
            const data = await this.api('/storage');
            if (data) this.storage = data;
        },

        async loadSmart() {
            this.smartStatus = '检查中...';
            const data = await this.api('/storage/smart');
            if (data) this.smartStatus = data;
        },

        async loadFirewall() {
            const data = await this.api('/firewall');
            if (data) this.firewallStatus = data;
        },

        async svcAction(name, action) {
            const data = await this.api(`/services/${name}/${action}`, { method: 'POST' });
            if (data) {
                this.showToast(data.message || '操作成功', 'success');
                this.loadServices();
            }
        },

        async showLogs(name) {
            this.logsService = name;
            this.logsContent = '加载中...';
            this.logsModal = true;
            const data = await this.api(`/services/${name}/logs`);
            if (data) this.logsContent = data || '无日志';
        },

        showAddUser() {
            this.addUserForm = { username: '', password: '' };
            this.addUserModal = true;
        },

        async addUser() {
            if (!this.addUserForm.username || !this.addUserForm.password) {
                this.showToast('请填写用户名和密码', 'error');
                return;
            }
            if (this.addUserForm.password.length < 12) {
                this.showToast('密码至少12位', 'error');
                return;
            }
            const data = await this.api('/users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.addUserForm.username)}&password=${encodeURIComponent(this.addUserForm.password)}`
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
                this.addUserModal = false;
                this.loadUsers();
            }
        },

        showChangePassword(username) {
            this.pwdUser = username;
            this.pwdForm = { password: '' };
            this.pwdModal = true;
        },

        async changePassword() {
            if (this.pwdForm.password.length < 12) {
                this.showToast('密码至少12位', 'error');
                return;
            }
            const data = await this.api(`/users/${this.pwdUser}/password`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `password=${encodeURIComponent(this.pwdForm.password)}`
            });
            if (data) {
                this.showToast(data.message || '修改成功', 'success');
                this.pwdModal = false;
            }
        },

        async deleteUser(username) {
            if (!confirm(`确定删除用户 ${username}？`)) return;
            const data = await this.api(`/users/${username}?delete_data=false`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '删除成功', 'success');
                this.loadUsers();
            }
        },

        async firewallAction(action) {
            if (!this.firewallForm.port) {
                this.showToast('请输入端口号', 'error');
                return;
            }
            const data = await this.api(`/firewall/${action}`, {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `port=${encodeURIComponent(this.firewallForm.port)}&proto=${encodeURIComponent(this.firewallForm.proto)}`
            });
            if (data) {
                this.showToast(data.message || '操作成功', 'success');
                this.loadFirewall();
            }
        },

        showToast(msg, type) {
            this.toast = { show: true, msg, type };
            setTimeout(() => { this.toast.show = false; }, 3000);
        },

        async loadMonitor() {
            const data = await this.api('/monitor');
            if (data) this.monitor = data;
        },

        async loadAlertConfig() {
            const data = await this.api('/alert-config');
            if (data && data.config) this.alertConfig = data.config;
        },

        async saveAlertConfig() {
            const params = new URLSearchParams();
            for (const [key, value] of Object.entries(this.alertConfig)) {
                params.append(key, value);
            }
            const data = await this.api('/alert-config', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: params.toString()
            });
            if (data) {
                this.showToast(data.message || '保存成功', 'success');
                this.loadMonitor();
            }
        }
    };
}

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
        monitorTimer: null,
        alertConfig: {},
        // Config module
        envConfig: {},
        sambaShares: [],
        vsftpdUsers: [],
        enabledServices: '',
        showAddShare: false,
        shareForm: { name: '', path: '', comment: '', valid_users: '', read_only: false },
        ftpUserForm: { username: '' },
        configFileForm: { name: '', content: '' },
        svcToggleForm: { service: '' },
        // Disk management
        diskInfo: '',
        diskMounts: '',
        diskLVM: '',
        diskIOStat: '',
        diskSmartDetail: '',
        diskPartitions: '',
        diskMkdirForm: { path: '' },
        mountForm: { device: '', mountpoint: '', fstype: '' },
        unmountForm: { target: '' },
        formatForm: { device: '', fstype: 'ext4' },
        // System settings
        sysNetwork: '',
        sysTime: '',
        hostnameForm: { hostname: '' },
        sysSSHConfig: '',
        sysSysctl: '',
        sysUpdates: '',
        sysEnabledServices: '',
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
                case 'monitor': this.initMonitorRefresh(); this.loadAlertConfig(); break;
                case 'config': this.loadEnvConfig(); break;
                case 'diskmgmt': break;
                case 'system': break;
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

        initMonitorRefresh() {
            if (this.monitorTimer) clearInterval(this.monitorTimer);
            this.loadMonitor();
            this.monitorTimer = setInterval(() => {
                if (this.page === 'monitor') this.loadMonitor();
            }, 180000);
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
        },

        // Config management
        async loadEnvConfig() {
            const data = await this.api('/config/env');
            if (data && data.config) this.envConfig = data.config;
        },

        async loadSambaShares() {
            const data = await this.api('/config/samba');
            if (data) this.sambaShares = data.shares || [];
        },

        async addSambaShare() {
            if (!this.shareForm.name || !this.shareForm.path) {
                this.showToast('共享名和路径必填', 'error'); return;
            }
            const data = await this.api('/config/samba/share', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.shareForm).toString()
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
                this.showAddShare = false;
                this.shareForm = { name: '', path: '', comment: '', valid_users: '', read_only: false };
                this.loadSambaShares();
            }
        },

        async deleteSambaShare(name) {
            if (!confirm(`确定删除共享 [${name}]？`)) return;
            const data = await this.api(`/config/samba/share?name=${name}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '删除成功', 'success');
                this.loadSambaShares();
            }
        },

        async loadVsftpdUsers() {
            const data = await this.api('/config/vsftpd-users');
            if (data) this.vsftpdUsers = data.users || [];
        },

        async addFtpUser() {
            if (!this.ftpUserForm.username) { this.showToast('请输入用户名', 'error'); return; }
            const data = await this.api('/config/vsftpd-users', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `username=${encodeURIComponent(this.ftpUserForm.username)}`
            });
            if (data) {
                this.showToast(data.message || '添加成功', 'success');
                this.ftpUserForm.username = '';
                this.loadVsftpdUsers();
            }
        },

        async removeFtpUser(username) {
            if (!confirm(`确定从 FTP 白名单移除 ${username}？`)) return;
            const data = await this.api(`/config/vsftpd-users?username=${username}`, { method: 'DELETE' });
            if (data) {
                this.showToast(data.message || '移除成功', 'success');
                this.loadVsftpdUsers();
            }
        },

        async loadConfigFile() {
            if (!this.configFileForm.name) return;
            this.configFileForm.content = '加载中...';
            const data = await this.api(`/config/file?name=${this.configFileForm.name}`);
            if (data) this.configFileForm.content = data.content || '';
        },

        async saveConfigFile() {
            const data = await this.api('/config/file', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `name=${this.configFileForm.name}&content=${encodeURIComponent(this.configFileForm.content)}`
            });
            if (data) this.showToast(data.message || '保存成功', 'success');
        },

        async loadEnabledServices() {
            this.enabledServices = '加载中...';
            const data = await this.api('/config/services');
            if (data) this.enabledServices = data;
        },

        async toggleService(action) {
            if (!this.svcToggleForm.service) { this.showToast('请输入服务名', 'error'); return; }
            const data = await this.api('/config/service-toggle', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `service=${this.svcToggleForm.service}&action=${action}`
            });
            if (data) {
                this.showToast(data.message || '操作成功', 'success');
                this.svcToggleForm.service = '';
            }
        },

        // Disk management
        async loadDiskInfo() {
            this.diskInfo = '加载中...';
            const data = await this.api('/disk/info');
            if (data) this.diskInfo = data;
        },

        async loadDiskMounts() {
            this.diskMounts = '加载中...';
            const data = await this.api('/disk/mounts');
            if (data) this.diskMounts = data;
        },

        async loadDiskLVM() {
            this.diskLVM = '加载中...';
            const data = await this.api('/disk/lvm');
            if (data) this.diskLVM = data;
        },

        async loadDiskIOStat() {
            this.diskIOStat = '测试中...(约3秒)';
            const data = await this.api('/disk/iostat');
            if (data) this.diskIOStat = data;
        },

        async loadDiskSmartDetail() {
            this.diskSmartDetail = '加载中...';
            const data = await this.api('/disk/smart-detail');
            if (data) this.diskSmartDetail = data;
        },

        async loadDiskPartitions() {
            this.diskPartitions = '加载中...';
            const data = await this.api('/disk/partitions');
            if (data) this.diskPartitions = data;
        },

        async diskMkdir() {
            if (!this.diskMkdirForm.path) { this.showToast('请输入路径', 'error'); return; }
            const data = await this.api('/disk/mkdir', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `path=${encodeURIComponent(this.diskMkdirForm.path)}`
            });
            if (data) {
                this.showToast(data.message || '创建成功', 'success');
                this.diskMkdirForm.path = '';
            }
        },

        async diskMount() {
            if (!this.mountForm.device || !this.mountForm.mountpoint) { this.showToast('设备和挂载点必填', 'error'); return; }
            const data = await this.api('/disk/mount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: new URLSearchParams(this.mountForm).toString()
            });
            if (data) {
                this.showToast(data.message || '挂载成功', 'success');
                this.loadDiskMounts();
            }
        },

        async diskUnmount() {
            if (!this.unmountForm.target) { this.showToast('请输入挂载点或设备', 'error'); return; }
            const data = await this.api('/disk/unmount', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `target=${encodeURIComponent(this.unmountForm.target)}`
            });
            if (data) {
                this.showToast(data.message || '卸载成功', 'success');
                this.unmountForm.target = '';
                this.loadDiskMounts();
            }
        },

        async diskFormat() {
            if (!this.formatForm.device) { this.showToast('请输入设备名', 'error'); return; }
            if (!confirm(`⚠️ 确定格式化 ${this.formatForm.device} 为 ${this.formatForm.fstype}？数据将全部丢失！`)) return;
            const data = await this.api('/disk/format', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `device=${encodeURIComponent(this.formatForm.device)}&fstype=${this.formatForm.fstype}&confirm=yes`
            });
            if (data) {
                this.showToast(data.message || '格式化成功', 'success');
                this.formatForm.device = '';
            }
        },

        // System settings
        async loadSysNetwork() {
            this.sysNetwork = '加载中...';
            const data = await this.api('/system/network');
            if (data) this.sysNetwork = data;
        },

        async loadSysTime() {
            this.sysTime = '加载中...';
            const data = await this.api('/system/time');
            if (data) this.sysTime = data;
        },

        async setHostname() {
            if (!this.hostnameForm.hostname) {
                this.showToast('请输入主机名', 'error');
                return;
            }
            const data = await this.api('/system/hostname', {
                method: 'POST',
                headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
                body: `hostname=${encodeURIComponent(this.hostnameForm.hostname)}`
            });
            if (data) {
                this.showToast(data.message || '修改成功', 'success');
                this.hostnameForm.hostname = '';
            }
        },

        async loadSysSSHConfig() {
            this.sysSSHConfig = '加载中...';
            const data = await this.api('/system/ssh-config');
            if (data) this.sysSSHConfig = data;
        },

        async loadSysSysctl() {
            this.sysSysctl = '加载中...';
            const data = await this.api('/system/sysctl');
            if (data) this.sysSysctl = data;
        },

        async loadSysUpdates() {
            this.sysUpdates = '检查中...';
            const data = await this.api('/system/updates');
            if (data) this.sysUpdates = data;
        },

        async loadSysEnabledServices() {
            this.sysEnabledServices = '加载中...';
            const data = await this.api('/system/services-enabled');
            if (data) this.sysEnabledServices = data;
        }
    };
}

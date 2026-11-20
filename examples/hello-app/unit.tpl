[Unit]
Description=hello-app (APP-SPEC 参考实现)
After=network.target

[Service]
# ExecStart 路径与核心落盘约定一致（web/modules/apps/registry.go appProgramDir +
# lifecycle.go writeAppFiles）：安装后程序目录是 /var/lib/nas/apps/<id>/，
# 包内 app/ 子树原样写入该目录下，所以二进制落在 <programDir>/app/hello。
ExecStart=/var/lib/nas/apps/hello/app/hello
Environment=PORT=__PORT__
Environment=DATA_DIR=__DATA_DIR__
Environment=NAS_SECRET=__NAS_SECRET__
Restart=on-failure
RestartSec=5

[Install]
WantedBy=multi-user.target

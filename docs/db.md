CREATE TABLE `tenants` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `name` varchar(128) NOT NULL COMMENT '租户名称（唯一，企业同名不可重复注册）',
  `status` tinyint NOT NULL DEFAULT '1',
  `quota_llm_token` bigint NOT NULL DEFAULT '0',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_tenants_name` (`name`),
  KEY `idx_tenants_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户表';

CREATE TABLE `users` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `username` varchar(64) NOT NULL,
  `password_hash` varchar(256) NOT NULL,
  `role` varchar(32) NOT NULL COMMENT 'admin / member',
  `status` tinyint NOT NULL DEFAULT '1',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_tenant_user` (`tenant_id`,`username`),
  KEY `idx_users_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户表';

CREATE TABLE `documents` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `user_id` bigint unsigned DEFAULT NULL COMMENT '上传者用户ID',
  `name` varchar(256) NOT NULL,
  `minio_object_key` varchar(512) NOT NULL,
  `status` varchar(32) NOT NULL COMMENT 'pending/processing/success/fail',
  `error_msg` text COMMENT '失败原因（仅 failed 状态时记录）',
  `size` bigint DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_documents_tenant_id` (`tenant_id`),
  KEY `idx_documents_user_id` (`user_id`),
  KEY `idx_documents_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='文档元数据表';

CREATE TABLE `agent_tasks` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `task_type` varchar(64) NOT NULL COMMENT '任务类型，document_parse',
  `biz_id` bigint unsigned DEFAULT NULL COMMENT '关联业务id，如 document id',
  `status` varchar(32) NOT NULL COMMENT 'pending/processing/success/failed',
  `error_msg` text COMMENT '错误信息（失败时记录）',
  `retry_count` int NOT NULL DEFAULT '0' COMMENT '已重试次数',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_agent_tasks_tenant_id` (`tenant_id`),
  KEY `idx_agent_tasks_biz_id` (`biz_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='agent异步任务';

CREATE TABLE `sessions` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `user_id` bigint unsigned NOT NULL,
  `title` varchar(256) DEFAULT NULL,
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  `deleted_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_sessions_tenant_id` (`tenant_id`),
  KEY `idx_sessions_user_id` (`user_id`),
  KEY `idx_sessions_deleted_at` (`deleted_at`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='会话元数据';

CREATE TABLE `audit_logs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `user_id` bigint unsigned DEFAULT NULL,
  `operation` varchar(128) NOT NULL,
  `trace_id` varchar(128) DEFAULT NULL,
  `content` text,
  `created_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  KEY `idx_audit_logs_tenant_id` (`tenant_id`),
  KEY `idx_audit_logs_user_id` (`user_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='审计日志';

CREATE TABLE `tenant_tool_configs` (
  `id` bigint unsigned NOT NULL AUTO_INCREMENT,
  `tenant_id` bigint unsigned NOT NULL,
  `tool_name` varchar(128) NOT NULL COMMENT '工具标识，与 Tool.Name() 对应',
  `is_enable` tinyint(1) DEFAULT NULL COMMENT '是否开启（bool 零值处理见代码注释，不加 default 以免关闭操作被列默认值覆盖）',
  `created_at` datetime(3) DEFAULT NULL,
  `updated_at` datetime(3) DEFAULT NULL,
  PRIMARY KEY (`id`),
  UNIQUE KEY `idx_tenant_tool` (`tenant_id`,`tool_name`),
  KEY `idx_tenant_tool_configs_tenant_id` (`tenant_id`)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='租户工具权限';
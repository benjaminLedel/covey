ALTER TABLE organizations
    DROP COLUMN IF EXISTS platform_repo_system,
    DROP COLUMN IF EXISTS platform_repo_project;

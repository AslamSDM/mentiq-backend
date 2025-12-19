-- Migration: Add project_members table for multi-user project support
-- This implements Option B: Account as organization, Users as team members

-- Create project_members table
CREATE TABLE IF NOT EXISTS project_members (
    id VARCHAR(255) PRIMARY KEY,
    project_id VARCHAR(255) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    role VARCHAR(50) NOT NULL DEFAULT 'member',
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    
    CONSTRAINT fk_project_members_project FOREIGN KEY (project_id) REFERENCES projects(id) ON DELETE CASCADE,
    CONSTRAINT fk_project_members_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE,
    CONSTRAINT unique_project_user UNIQUE (project_id, user_id)
);

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_project_members_project_id ON project_members(project_id);
CREATE INDEX IF NOT EXISTS idx_project_members_user_id ON project_members(user_id);
CREATE INDEX IF NOT EXISTS idx_project_members_role ON project_members(role);

-- Add new columns to users table if they don't exist
ALTER TABLE users ADD COLUMN IF NOT EXISTS full_name VARCHAR(255);
ALTER TABLE users ADD COLUMN IF NOT EXISTS role VARCHAR(50) DEFAULT 'member';
ALTER TABLE users ADD COLUMN IF NOT EXISTS is_active BOOLEAN DEFAULT true;

-- Add description column to projects table if it doesn't exist
ALTER TABLE projects ADD COLUMN IF NOT EXISTS description TEXT;

-- Update Account password field to not be exposed (handled at model level with json:"-")
-- No SQL changes needed for this

-- Migration to create first user for each existing account and add them as project owners
-- This ensures backward compatibility for existing accounts
DO $$
DECLARE
    acc RECORD;
    new_user_id VARCHAR(255);
BEGIN
    FOR acc IN SELECT id, email, password, name FROM accounts WHERE password IS NOT NULL AND password != ''
    LOOP
        -- Create a user for this account if one doesn't exist
        SELECT id INTO new_user_id FROM users WHERE account_id = acc.id AND email = acc.email LIMIT 1;
        
        IF new_user_id IS NULL THEN
            new_user_id := gen_random_uuid()::text;
            
            INSERT INTO users (id, email, password, full_name, role, is_active, account_id, created_at, updated_at)
            VALUES (
                new_user_id,
                acc.email,
                acc.password,
                acc.name,
                'owner',
                true,
                acc.id,
                CURRENT_TIMESTAMP,
                CURRENT_TIMESTAMP
            );
            
            RAISE NOTICE 'Created user % for account %', new_user_id, acc.id;
        END IF;
        
        -- Add this user as owner to all projects in this account
        INSERT INTO project_members (id, project_id, user_id, role, created_at, updated_at)
        SELECT 
            gen_random_uuid()::text,
            p.id,
            new_user_id,
            'owner',
            CURRENT_TIMESTAMP,
            CURRENT_TIMESTAMP
        FROM projects p
        WHERE p.account_id = acc.id
        ON CONFLICT (project_id, user_id) DO NOTHING;
        
        RAISE NOTICE 'Added user % as owner to all projects for account %', new_user_id, acc.id;
    END LOOP;
END $$;

-- Comments for documentation
COMMENT ON TABLE project_members IS 'Join table linking users to projects with role-based permissions';
COMMENT ON COLUMN project_members.role IS 'User role in project: owner, admin, member, viewer';
COMMENT ON COLUMN users.role IS 'User role in account: owner, admin, member, viewer';
COMMENT ON COLUMN users.full_name IS 'Full name of the user';
COMMENT ON COLUMN users.is_active IS 'Whether the user account is active';

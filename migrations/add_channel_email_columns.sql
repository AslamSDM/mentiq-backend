-- Add channel and email columns to events table
-- Run this migration to add channel tracking and email support

-- Add channel column
ALTER TABLE events ADD COLUMN IF NOT EXISTS channel VARCHAR(255);

-- Add email column
ALTER TABLE events ADD COLUMN IF NOT EXISTS email VARCHAR(255);

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_events_channel ON events(channel);
CREATE INDEX IF NOT EXISTS idx_events_email ON events(email);

-- Optional: Add composite indexes for common queries
CREATE INDEX IF NOT EXISTS idx_events_channel_project ON events(channel, project_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_events_email_user ON events(email, user_id);

-- Verify columns were added
SELECT column_name, data_type, is_nullable 
FROM information_schema.columns 
WHERE table_name = 'events' 
AND column_name IN ('channel', 'email');

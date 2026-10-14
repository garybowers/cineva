-- A device can sign itself in (see docs/devices.md): whenever nobody is
-- signed in on it, the living-room TV becomes the user an admin chose,
-- and anyone can still switch to themselves with their PIN or password.
-- '' for a device that shows the sign-in screen as usual.
ALTER TABLE devices ADD COLUMN auto_user_id TEXT NOT NULL DEFAULT '';

CREATE TRIGGER devices_auto_user_deleted AFTER DELETE ON users BEGIN
    UPDATE devices SET auto_user_id = '' WHERE auto_user_id = OLD.id;
END;

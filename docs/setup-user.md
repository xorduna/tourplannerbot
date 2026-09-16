To create a new user and database for the Tour Planner Bot, you can use the following SQL commands. Make sure to replace 'your_strong_password' with a secure password of your choice.

```sql
CREATE USER tourplannerbot WITH PASSWORD 'your_strong_password';
CREATE DATABASE tourplannerbot OWNER tourplannerbot;
GRANT ALL PRIVILEGES ON DATABASE tourplannerbot TO tourplannerbot;
```

Env var `DATABASE_URL` should then be set to:

```postgres://tourplannerbot:your_strong_password@localhost:5432/tourplannerbot```


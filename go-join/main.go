package main

import (
    "fmt"
    "os"
    "log"
    "strings"
    "flag"
    "time"
    "exec"
)

const (
    // go-connect and go-join must be on same host for now,
    /// but in future go-connect could be remote
    GO_HOST      = "127.0.0.1:8790"
	RPL_NAMREPLY = "353"
    CHANNEL_CMDS = "PRIVMSG, PART, JOIN, " + RPL_NAMREPLY
    NOTIFY_CMD = "/usr/bin/notify-send"
    SOUND_CMD = "/usr/bin/aplay -q /home/graham/SpiderOak/xchat_sounds/beep.wav"
    USAGE = `
Usage: go-join [channel|-private=nick]
Note there's no # in front of the channel
Examples:
 1. Join channel test: go-join test
 2. Listen for private (/query) message from bob: go-join -private=bob
`

)

var userPrivate = flag.String(
    "private",
    "",
    "Listen for private messages from this nick only")

var fromUser = make(chan []byte)
var fromServer = make(chan []byte)

// Logs messages from go-connect
var rawLog *log.Logger;

func init() {
    var logfile *os.File;
    logfile, _ = os.Create("/tmp/go-join.log");
    rawLog = log.New(logfile, "", log.LstdFlags);
}

/*
 * main
 */
func main() {

    if len(os.Args) != 2 {
        fmt.Println(USAGE)
        os.Exit(1)
    }

    var channel string

    arg := os.Args[1]
    if strings.HasPrefix(arg, "-private") {
        flag.Parse()
        channel = *userPrivate
    } else {
        channel = "#" + arg
    }

    client := NewClient(channel)
    defer func() {
        client.Close()
        fmt.Println("Bye!")
    }()

    client.Run()
}

// IRC Client abstraction
type Client struct {
    term *Terminal
    conn *InternalConnection
    channel string
    isRunning bool
    nick string
}

// Create IRC client. Switch keyboard to raw mode, connect to go-connect socket
func NewClient(channel string) *Client {

    // Set terminal to raw mode, listen for keyboard input
    var term *Terminal = NewTerminal()
    term.Raw()
    term.Channel = channel

    // Connect to go-connect
    var conn *InternalConnection
    conn = NewInternalConnection(GO_HOST, channel)

    return &Client{term: term, conn: conn, channel: channel}
}

/* Main loop
   Listen for keyboard input and socket input and be an IRC client
*/
func (self *Client) Run() {

    self.isRunning = true

    go self.term.ListenInternalKeys()
    self.display("Listening for keyboard input")

	go self.conn.Consume()
    self.display("Connected to go-connect")

    if strings.HasPrefix(self.channel, "#") {
        self.display("Joining channel " + self.channel)
    } else {
        self.display("Listening for private messages from " + self.channel)
    }

    for self.isRunning {

        select {
            case serverData := <-fromServer:
                rawLog.Println(string(serverData))
                self.onServer(serverData)

            case userInput := <-fromUser:
                self.onUser(userInput)
        }
    }

    // Internal listener for user input from socket
	//go listenInternalSocket()
	//fmt.Println("Use 'netcat 127.0.0.1 " + INTERNAL_PORT + "' to connect for writes")

    return
}

// Do something with user input. Usually just send to go-connect
func (self *Client) onUser(content []byte) {

    if string(content) == "/quit" {
        // Local quit, don't send to server
        // Currently there's no global quit
        self.isRunning = false
        return
    }

    // /me is really a message pretending to be a command,
    isMeCommand := strings.HasPrefix(string(content), "/me")

    if isCommand(content) && ! isMeCommand {
        // IRC command
        self.conn.Write(content)

    } else {
        // Send to go-connect
        self.conn.Write([]byte(self.channel + " "))
        self.conn.Write(content)

        // Display locally

        if isMeCommand {
            content = content[4:]
            self.displayAction(self.nick, string(content))

        } else {
            line := Line{
                Received: time.LocalTime().Format(time.RFC3339),
                User: self.nick,
                Content: string(content),
                Channel: self.channel,
                IsCTCP: isMeCommand}
            self.term.Write([]byte(line.String(self.nick)))
        }
    }

}

// Do something with Line from go-connect. Usually just display to screen.
func (self *Client) onServer(serverData []byte) {

    line := FromJson(serverData)

    isWrongChannel := strings.Contains(CHANNEL_CMDS, line.Command) &&
                      line.Channel != self.channel
    isPrivateMsg := line.Channel == line.User
    if isWrongChannel && !isPrivateMsg {
        return
    }

    switch line.Command {

        case "PRIVMSG":
            self.term.Write([]byte(line.String(self.nick)))
            if strings.Contains(line.Content, self.nick) || isPrivateMsg {
                self.Notify(line)
            }

        case "ACTION": self.displayAction(line.User, line.Content)

        case "JOIN": self.display(line.User + " joined the channel")

        case RPL_NAMREPLY:
            self.display("Users currently in " + line.Channel + ": ")
            self.display(line.Content)

        case "NICK":
            if len(line.User) == 0 || line.User == self.nick {
                self.nick = line.Content
                self.display("You are now know as " + self.nick)
            } else {
                self.display(line.User + " is now know as " + line.Content)
            }

        case "PART": self.display(line.User + " left the channel.")

        case "QUIT": self.display(line.User + " has quit.")
    }

}

// Write string to terminal
func (self *Client) display(msg string) {
    self.term.Write([]byte(msg + "\n\r"))
}

// Write an action to the terminal  TODO: This duplicates some of line.String
func (self *Client) displayAction(nick, content string) {
    var formatted string
    if nick == self.nick {
        formatted = Bold(" * " + nick)
    } else {
        formatted = colorfullUser(nick, " * " + nick)
    }

    self.display(formatted + " " + content)
}

// Alert user that someone is talking to them
func (self *Client) Notify(line *Line) {

    title := line.User
    // Private message have Channel == User so don't repeat it
    if line.Channel != line.User {
        title += " " + line.Channel
    }
    notifyCmd := exec.Command(NOTIFY_CMD, title, line.Content)
    notifyCmd.Run()

    parts := strings.Split(SOUND_CMD, " ")
    soundCmd := exec.Command(parts[0], parts[1:]...)
    soundCmd.Run()
}

func (self *Client) Close() os.Error {
    self.term.Close()
    return self.conn.Close()
}

// Is 'content' an IRC command?
func isCommand(content []byte) bool {
	return len(content) > 1 && content[0] == '/'
}